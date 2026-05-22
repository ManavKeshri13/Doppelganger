// llm.go — Project Aegis
//
// LLM integration layer — Groq (llama-3.3-70b-versatile).
//
// Groq's API is fully OpenAI-compatible (same JSON schema for
// /v1/chat/completions), so we target it with a single thin
// HTTP client.  No SDK dependency required.
//
// The sole exported function is AnalyzePage, which sends the
// raw text of a webpage to the LLM and returns a structured
// AnalysisPayload describing detected dark patterns and
// privacy risks.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// AnalysisPayload is the canonical structure the LLM returns for every scan.
type AnalysisPayload struct {
	RiskScore        int      `json:"risk_score"`
	DarkPatterns     []string `json:"dark_patterns"`
	Summary          string   `json:"summary"`
	ConsentViolated  bool     `json:"consent_violated"`
	ViolationDetails string   `json:"violation_details"`
}

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

// buildSystemPrompt returns the instruction that tells the LLM exactly what
// to look for and what JSON structure to produce.
// If consentRules is non-empty the Gatekeeper section is appended, instructing
// the model to cross-reference the site's data harvesting against the user's
// personal privacy boundaries.
func buildSystemPrompt(consentRules string) string {
	base := `You are an expert in digital privacy, UX ethics, and consumer protection.
Your task is to analyse the text content of a webpage and identify "dark patterns"
and privacy risks present in the content.

Dark patterns to look for include (but are not limited to):
- Trick questions or confusing double-negatives in consent forms
- Hidden subscription auto-renewals or pre-ticked opt-in boxes
- Guilt-trip or shaming language on cancel/decline buttons ("No thanks, I hate saving money")
- Urgency or scarcity manipulation ("Only 1 left!", countdown timers that reset)
- Roach motel: easy to sign up, hidden/difficult cancellation
- Privacy Zuckering: confusing privacy settings designed to maximise data sharing
- Misdirection: visual design that draws attention away from important information
- Disguised ads presented as organic content
- Excessive data collection requests (location, contacts, camera) beyond what is needed
- Tracking pixels, fingerprinting scripts, or third-party data sharing references

STRICT RULES:
1. Return ONLY a single valid JSON object – no markdown fences, no prose, no explanation.
2. The JSON must follow this exact schema:
   {
     "risk_score": <integer 1-10, where 1 = very safe, 10 = extremely manipulative>,
     "dark_patterns": ["<pattern 1>", "<pattern 2>", ...],
     "summary": "<A 1-3 sentence plain-English summary of the key privacy risks and manipulative techniques found>",
     "consent_violated": <true | false>,
     "violation_details": "<If consent_violated is true, explain exactly which boundary was crossed and how. Empty string if false.>"
   }
3. If no dark patterns are found, return an empty array for dark_patterns and a risk_score of 1-2.
4. Be specific and factual — only flag patterns you can directly infer from the text.
5. Do NOT include anything outside the JSON object.`

	if consentRules == "" {
		// No personal rules provided — consent fields default to safe values
		base += `
6. Since no personal consent rules were provided by the user, always set:
   "consent_violated": false,
   "violation_details": ""`
		return base
	}

	base += fmt.Sprintf(`

== GATEKEEPER: PERSONAL CONSENT RULES ==
The user has defined the following personal privacy boundaries:
"""
%s
"""

You MUST perform a Gatekeeper check:
- Read the page text carefully for any data the site is collecting, requiring, or attempting to harvest.
- Cross-reference that data against the user's consent rules above.
- If the site requests, requires, or harvests ANY data type that the user has explicitly stated they do NOT consent to sharing, set "consent_violated" to true.
- In "violation_details", describe specifically: (a) what data the site is trying to collect, and (b) which exact rule from the user's list it violates.
- If no boundary is crossed, set "consent_violated" to false and "violation_details" to "".`, consentRules)

	return base
}

// ---------------------------------------------------------------------------
// Main exported function
// ---------------------------------------------------------------------------

// AnalyzePage calls the Groq API with the given page text and optional
// consentRules and returns a populated AnalysisPayload.
// When consentRules is non-empty the Gatekeeper check is activated.
func AnalyzePage(cfg *Config, pageText, consentRules string) (*AnalysisPayload, error) {
	log.Printf("[LLM] Requesting dark-pattern analysis from Groq (model: llama-3.3-70b-versatile)")
	log.Printf("[LLM] Page text length: %d chars | Consent rules provided: %v", len(pageText), consentRules != "")
	return callGroq(cfg, pageText, consentRules)
}

// ---------------------------------------------------------------------------
// Groq  (OpenAI-compatible chat completions endpoint)
// ---------------------------------------------------------------------------

const (
	groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	groqModel    = "llama-3.3-70b-versatile"
	// Cap page text to avoid exceeding the model's context window.
	maxPageTextChars = 12000
)

// groqRequest is the request body for the OpenAI-compatible completions API.
type groqRequest struct {
	Model          string              `json:"model"`
	Messages       []groqMessage       `json:"messages"`
	Temperature    float64             `json:"temperature"`
	MaxTokens      int                 `json:"max_tokens"`
	ResponseFormat *groqResponseFormat `json:"response_format,omitempty"`
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

// groqResponse is a partial mirror of the OpenAI-compatible response body.
type groqResponse struct {
	Choices []struct {
		Message groqMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func callGroq(cfg *Config, pageText, consentRules string) (*AnalysisPayload, error) {
	// Truncate page text to stay within token limits
	if len(pageText) > maxPageTextChars {
		pageText = pageText[:maxPageTextChars] + "\n\n[... content truncated for analysis ...]"
	}

	reqBody := groqRequest{
		Model: groqModel,
		Messages: []groqMessage{
			{
				Role:    "system",
				Content: buildSystemPrompt(consentRules),
			},
			{
				Role:    "user",
				Content: "Analyse the following webpage text for dark patterns and privacy risks:\n\n" + pageText,
			},
		},
		Temperature:    0.2, // Low temperature for consistent, factual analysis
		MaxTokens:      1024,
		ResponseFormat: &groqResponseFormat{Type: "json_object"},
	}

	raw, err := postJSON(groqEndpoint, reqBody, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + cfg.LLMApiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("groq HTTP error: %w", err)
	}

	var resp groqResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("groq parse error: %w\nBody: %s", err, string(raw))
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("groq API error [%v]: %s", resp.Error.Code, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("groq returned no choices")
	}

	text := resp.Choices[0].Message.Content
	return parseAnalysisJSON(text)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// postJSON serialises body as JSON, POSTs it to url with the given headers,
// and returns the raw response bytes.
func postJSON(url string, body any, headers map[string]string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	log.Printf("[LLM] HTTP %d — received %d bytes", resp.StatusCode, len(raw))
	return raw, nil
}

// jsonFenceRe strips optional ``` fences that some models add despite instructions.
var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// parseAnalysisJSON extracts an AnalysisPayload from the LLM's raw text output.
// It handles both bare JSON and JSON wrapped in markdown code fences.
func parseAnalysisJSON(text string) (*AnalysisPayload, error) {
	text = strings.TrimSpace(text)

	// Strip markdown fences if present
	if matches := jsonFenceRe.FindStringSubmatch(text); len(matches) == 2 {
		text = matches[1]
	}

	var payload AnalysisPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal LLM JSON: %w\nRaw text: %s", err, text)
	}

	// Clamp risk score to valid range
	if payload.RiskScore < 1 {
		payload.RiskScore = 1
	}
	if payload.RiskScore > 10 {
		payload.RiskScore = 10
	}

	// Ensure dark_patterns is never nil (use empty slice for clean JSON)
	if payload.DarkPatterns == nil {
		payload.DarkPatterns = []string{}
	}

	log.Printf("[LLM] ✓ Risk Score: %d/10", payload.RiskScore)
	log.Printf("[LLM] ✓ Dark Patterns (%d): %v", len(payload.DarkPatterns), payload.DarkPatterns)
	log.Printf("[LLM] ✓ Summary: %s", payload.Summary)
	log.Printf("[LLM] ✓ Consent Violated: %v | Details: %s", payload.ConsentViolated, payload.ViolationDetails)

	return &payload, nil
}
