// server.go — Project Aegis
//
// Local REST API server built on net/http.
// Exposes two endpoints consumed by the Chrome extension:
//
//   GET  /status   – live engine metrics (risk score, dark patterns, phase)
//   POST /analyze  – submit page text for dark-pattern analysis (async)
//
// CORS middleware is included so the Chrome extension (chrome-extension://...)
// and any local dev page can call the API without browser CORS errors.

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Response DTOs
// ---------------------------------------------------------------------------

// StatusResponse is the JSON body returned by GET /status.
type StatusResponse struct {
	Status           string    `json:"status"`
	RiskScore        int       `json:"riskScore"`
	DarkPatterns     []string  `json:"darkPatterns"`
	Summary          string    `json:"summary"`
	TotalScans       int       `json:"totalScans"`
	LastScanTime     time.Time `json:"lastScanTime"`
	ConsentViolated  bool      `json:"consentViolated"`
	ViolationDetails string    `json:"violationDetails"`
	LastError        string    `json:"lastError,omitempty"`
}

// AnalyzeRequest is the JSON body expected by POST /analyze.
type AnalyzeRequest struct {
	PageText     string `json:"page_text"`
	ConsentRules string `json:"consent_rules"` // optional Gatekeeper rules
}

// AnalyzeResponse is the JSON body returned by POST /analyze.
type AnalyzeResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ErrorResponse wraps an error message as JSON.
type ErrorResponse struct {
	Error string `json:"error"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server owns the HTTP mux and has references to all application dependencies.
type Server struct {
	cfg   *Config
	state *EngineState
	mux   *http.ServeMux
}

// NewServer wires up the HTTP routes and returns a ready-to-run Server.
func NewServer(cfg *Config, state *EngineState) *Server {
	s := &Server{
		cfg:   cfg,
		state: state,
		mux:   http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Run starts the HTTP server on the given address (e.g. ":8000").
func (s *Server) Run(addr string) error {
	handler := corsMiddleware(s.mux)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

func (s *Server) registerRoutes() {
	// Health / status polling
	s.mux.HandleFunc("/status", s.handleStatus)

	// Main analysis endpoint
	s.mux.HandleFunc("/analyze", s.handleAnalyze)

	// Catch-all for unknown routes
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "not found"})
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleStatus responds to GET /status with a snapshot of the engine state.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "method not allowed"})
		return
	}

	snap := s.state.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

// handleAnalyze responds to POST /analyze.
//
// The actual LLM call is dispatched in a goroutine so the HTTP response
// returns immediately (the extension polls /status for progress).
// If an analysis is already running, the request is rejected with 409 Conflict.
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "method not allowed"})
		return
	}

	// Guard: refuse concurrent sessions
	snap := s.state.Snapshot()
	if snap.Status == string(StatusAnalyzing) {
		writeJSON(w, http.StatusConflict, AnalyzeResponse{
			Success: false,
			Message: "An analysis is already running. Please wait.",
		})
		return
	}

	// Read and parse the request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2 MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "failed to read request body"})
		return
	}

	var req AnalyzeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	if req.PageText == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "page_text is required"})
		return
	}

	log.Printf("[SERVER] /analyze received — page text: %d chars | consent rules: %v", len(req.PageText), req.ConsentRules != "")

	// Acknowledge immediately; analysis runs in background
	writeJSON(w, http.StatusAccepted, AnalyzeResponse{
		Success: true,
		Message: "🔍 Analysis started. Poll /status for results.",
	})

	// Run the LLM analysis in the background
	go s.runAnalysisSession(req.PageText, req.ConsentRules)
}

// ---------------------------------------------------------------------------
// Analysis session orchestration
// ---------------------------------------------------------------------------

// runAnalysisSession is the goroutine that:
//  1. Marks state as "analyzing"
//  2. Calls the LLM to detect dark patterns and run the Gatekeeper check
//  3. Stores the results back into EngineState
func (s *Server) runAnalysisSession(pageText, consentRules string) {
	log.Println("[ENGINE] 🔍 Starting dark-pattern analysis...")
	s.state.SetAnalyzing()

	payload, err := AnalyzePage(s.cfg, pageText, consentRules)
	if err != nil {
		log.Printf("[ENGINE] ❌ LLM error: %v", err)
		s.state.SetError(err.Error())
		return
	}

	s.state.SetResults(payload)
	log.Printf("[ENGINE] ✅ Analysis complete — risk score: %d/10, patterns found: %d, consent violated: %v",
		payload.RiskScore, len(payload.DarkPatterns), payload.ConsentViolated)
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// corsMiddleware adds the CORS headers required for the Chrome extension
// (origin: chrome-extension://<id>) to communicate with the local server.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle pre-flight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeJSON serialises v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[HTTP] Failed to encode response: %v", err)
	}
}
