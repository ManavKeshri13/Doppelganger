// state.go — Project Aegis
//
// EngineState is the single source of truth for all runtime metrics.
// It is intentionally kept simple — a mutex-protected struct — so that
// it can be shared safely between the HTTP handlers and the analysis
// goroutine without importing any additional packages.

package main

import (
	"sync"
	"time"
)

// EngineStatus represents the current lifecycle phase of the analysis engine.
type EngineStatus string

const (
	StatusIdle     EngineStatus = "idle"
	StatusAnalyzing EngineStatus = "analyzing"
	StatusError    EngineStatus = "error"
)

// EngineState is thread-safe runtime state shared across the application.
type EngineState struct {
	mu sync.RWMutex

	// Status is the current lifecycle phase.
	Status EngineStatus

	// RiskScore is the privacy risk score (1–10) from the last scan.
	RiskScore int

	// DarkPatterns is the list of detected manipulative UX patterns.
	DarkPatterns []string

	// Summary is the plain-English description of the privacy risks found.
	Summary string

	// LastScanTime records when the most recent /analyze request was received.
	LastScanTime time.Time

	// TotalScans is the cumulative count of page analyses performed.
	TotalScans int

	// LastError stores the most recent error message (empty if none).
	LastError string
}

// NewEngineState returns an EngineState ready for use.
func NewEngineState() *EngineState {
	return &EngineState{
		Status:       StatusIdle,
		DarkPatterns: []string{},
	}
}

// ---------------------------------------------------------------------------
// Thread-safe accessors / mutators
// ---------------------------------------------------------------------------

// SetAnalyzing transitions into the analyzing phase.
func (s *EngineState) SetAnalyzing() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = StatusAnalyzing
	s.LastScanTime = time.Now()
	s.LastError = ""
}

// SetResults stores the completed analysis results and transitions back to idle.
func (s *EngineState) SetResults(payload *AnalysisPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Status = StatusIdle
	s.RiskScore = payload.RiskScore
	s.TotalScans++

	// Defensive copy of the slice
	patterns := make([]string, len(payload.DarkPatterns))
	copy(patterns, payload.DarkPatterns)
	s.DarkPatterns = patterns

	s.Summary = payload.Summary
}

// SetError records an error and transitions to the error phase.
func (s *EngineState) SetError(errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = StatusError
	s.LastError = errMsg
}

// Snapshot returns an immutable view of the current state for the /status endpoint.
func (s *EngineState) Snapshot() StatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	patterns := make([]string, len(s.DarkPatterns))
	copy(patterns, s.DarkPatterns)

	return StatusResponse{
		Status:       string(s.Status),
		RiskScore:    s.RiskScore,
		DarkPatterns: patterns,
		Summary:      s.Summary,
		LastScanTime: s.LastScanTime,
		TotalScans:   s.TotalScans,
		LastError:    s.LastError,
	}
}
