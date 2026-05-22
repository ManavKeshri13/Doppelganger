// llm.go — Project Doppelgänger
//
// LLM integration layer — Groq (llama-3.3-70b-versatile).
//
// Groq's API is fully OpenAI-compatible (same JSON schema for
// /v1/chat/completions), so we target it with a single thin
// HTTP client.  No SDK dependency required.
//
// The sole exported function is GeneratePersonaAndQueries, which
// asks the model to produce a realistic browsing persona and a
// list of search queries that persona would plausibly run.

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
// Shared types
// ---------------------------------------------------------------------------

// PersonaPayload is the canonical structure we expect the LLM to return.
type PersonaPayload struct {
	Persona string   `json:"persona"`
	Queries []string `json:"queries"`
}

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

// buildSystemPrompt returns the instruction that tells the LLM exactly what
// JSON structure to produce.  n controls how many queries are requested.
func buildSystemPrompt(n int) string {
	return fmt.Sprintf(`You are a privacy-persona generator for an academic research tool.
Your task is to produce a hyper-realistic synthetic internet user and a set of search
queries they would naturally perform today.

STRICT RULES:
1. Return ONLY a single valid JSON object – no markdown fences, no prose, no explanation.
2. The JSON must follow this exact schema:
   {
     "persona": "<A detailed, realistic demographic description, e.g. '34-year-old freelance graphic designer in Austin, TX, interested in typography, home espresso brewing, and Formula 1 racing'>",
     "queries": ["<query 1>", "<query 2>", ..., "<query %d>"]
   }
3. Queries must be natural, varied, and plausible for the persona – mix of
   informational, navigational, and commercial intent.
4. Do NOT include anything outside the JSON object.`, n)
}

// ---------------------------------------------------------------------------
// Main exported function
// ---------------------------------------------------------------------------

// GeneratePersonaAndQueries calls the Groq API and returns a populated
// PersonaPayload containing a synthetic persona and search queries.
func GeneratePersonaAndQueries(cfg *Config) (*PersonaPayload, error) {
	log.Printf("[LLM] Requesting persona from Groq (model: llama-3.3-70b-versatile)")
	return callGroq(cfg)
}

// ---------------------------------------------------------------------------
// Groq  (OpenAI-compatible chat completions endpoint)
// ---------------------------------------------------------------------------

const (
	groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	groqModel    = "llama-3.3-70b-versatile"
)

// groqRequest is the request body for the OpenAI-compatible completions API.
type groqRequest struct {
	Model       string        `json:"model"`
	Messages    []groqMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	// Ask the model to return valid JSON — supported by Groq's Llama endpoint
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

func callGroq(cfg *Config) (*PersonaPayload, error) {
	reqBody := groqRequest{
		Model: groqModel,
		Messages: []groqMessage{
			{
				Role:    "system",
				Content: buildSystemPrompt(cfg.QueriesPerSession),
			},
			{
				Role:    "user",
				Content: "Generate a new persona and search queries now.",
			},
		},
		Temperature: 0.9,
		MaxTokens:   512,
		// Instruct the model to guarantee a JSON object in its output
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
	return parsePersonaJSON(text)
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

// parsePersonaJSON extracts a PersonaPayload from the LLM's raw text output.
// It handles both bare JSON and JSON wrapped in markdown code fences.
func parsePersonaJSON(text string) (*PersonaPayload, error) {
	text = strings.TrimSpace(text)

	// Strip markdown fences if present
	if matches := jsonFenceRe.FindStringSubmatch(text); len(matches) == 2 {
		text = matches[1]
	}

	var payload PersonaPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal LLM JSON: %w\nRaw text: %s", err, text)
	}

	if payload.Persona == "" {
		return nil, fmt.Errorf("LLM returned empty persona field")
	}
	if len(payload.Queries) == 0 {
		return nil, fmt.Errorf("LLM returned no queries")
	}

	log.Printf("[LLM] ✓ Persona: %s", payload.Persona)
	log.Printf("[LLM] ✓ Queries (%d): %v", len(payload.Queries), payload.Queries)
	return &payload, nil
}
