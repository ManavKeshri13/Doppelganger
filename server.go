// server.go — Project Doppelgänger
//
// Local REST API server built on net/http.
// Exposes two endpoints consumed by the Chrome extension:
//
//   GET  /status   – live engine metrics (persona, counters, phase)
//   POST /trigger  – fire a new noise-injection session asynchronously
//
// CORS middleware is included so the Chrome extension (chrome-extension://...)
// and any local dev page can call the API without browser CORS errors.

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Response DTOs
// ---------------------------------------------------------------------------

// StatusResponse is the JSON body returned by GET /status.
type StatusResponse struct {
	Status               string    `json:"status"`
	ActivePersona        string    `json:"activePersona"`
	ActiveQueries        []string  `json:"activeQueries"`
	TotalQueriesInjected int       `json:"totalQueriesInjected"`
	LastTriggerTime      time.Time `json:"lastTriggerTime"`
	LastError            string    `json:"lastError,omitempty"`
}

// TriggerResponse is the JSON body returned by POST /trigger.
type TriggerResponse struct {
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
	// Health / meta
	s.mux.HandleFunc("/status", s.handleStatus)

	// Main action endpoint
	s.mux.HandleFunc("/trigger", s.handleTrigger)

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

// handleTrigger responds to POST /trigger.
//
// The actual LLM call + browser automation is dispatched in a goroutine so
// that the HTTP response returns immediately (the extension polls /status for
// progress).  If the engine is already running, the request is rejected with
// 409 Conflict.
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "method not allowed"})
		return
	}

	// Guard: refuse concurrent sessions
	snap := s.state.Snapshot()
	if snap.Status == string(StatusRunning) {
		writeJSON(w, http.StatusConflict, TriggerResponse{
			Success: false,
			Message: "A noise-injection session is already running. Please wait.",
		})
		return
	}

	// Acknowledge the request immediately
	writeJSON(w, http.StatusAccepted, TriggerResponse{
		Success: true,
		Message: "🕵️ Noise injection initiated. Poll /status for progress.",
	})

	// Run the heavy lifting in the background
	go s.runNoiseSession()
}

// ---------------------------------------------------------------------------
// Noise session orchestration
// ---------------------------------------------------------------------------

// runNoiseSession is the goroutine that:
//  1. Calls the LLM to generate a persona + queries
//  2. Hands off to the browser automation engine (Phase 2)
//  3. Updates EngineState throughout
func (s *Server) runNoiseSession() {
	log.Println("[ENGINE] 🚀 Starting noise session...")

	// --- Step 1: Generate persona and queries via LLM ---
	payload, err := GeneratePersonaAndQueries(s.cfg)
	if err != nil {
		log.Printf("[ENGINE] ❌ LLM error: %v", err)
		s.state.SetError(err.Error())
		return
	}

	// Trim queries to the configured limit (LLM might return more)
	queries := payload.Queries
	if len(queries) > s.cfg.QueriesPerSession {
		queries = queries[:s.cfg.QueriesPerSession]
	}

	s.state.SetRunning(payload.Persona, queries)

	// --- Step 2: Execute headless browsing (Phase 2 – browser.go) ---
	injected, err := RunBrowsingSession(s.cfg, queries)
	if err != nil {
		log.Printf("[ENGINE] ❌ Browser error: %v", err)
		s.state.SetError(err.Error())
		return
	}

	// --- Step 3: Update counters and return to idle ---
	s.state.IncrementInjected(injected)
	s.state.SetIdle()

	log.Printf("[ENGINE] ✅ Session complete – %d queries injected (total: %d)",
		injected, s.state.Snapshot().TotalQueriesInjected)
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// corsMiddleware adds the CORS headers required for the Chrome extension
// (origin: chrome-extension://<id>) to communicate with the local server.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow any origin – this API is only reachable on localhost
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
