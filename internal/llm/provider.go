// Package llm provides a provider-pluggable LLM client and higher-level services
// for gitstate: diff-difficulty estimation (decisions P3) and status synthesis
// (roadmap §3 reporting). The Anthropic implementation calls the Messages API
// directly via stdlib net/http — no SDK dependency — making it easy to add
// OpenAI or other providers later by implementing the Provider interface.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrLLMNotConfigured is returned when no API key is available. Callers handle
// this gracefully (e.g. skip writing an estimate) rather than crashing.
var ErrLLMNotConfigured = errors.New("llm: not configured (AnthropicAPIKey is empty)")

// Provider is the minimal interface a language model backend must satisfy.
// Implementing this interface is all that's needed to swap in a new provider.
type Provider interface {
	// Complete sends a system prompt and a user prompt to the model and returns
	// the raw text of the first content block.
	Complete(ctx context.Context, system, user string) (string, error)
}

// ─── Anthropic implementation ──────────────────────────────────────────────

const (
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion     = "2023-06-01"
	defaultHTTPTimeout   = 60 * time.Second
	// maxOutputTokens is a safe ceiling for the structured JSON responses we
	// request; well within any model's limit and keeps costs bounded.
	maxOutputTokens = 1024
)

// anthropicClient calls the Anthropic Messages API using stdlib net/http.
// Adding an OpenAI or local-model provider later requires only a new struct
// that implements Provider — no changes to Service or callers.
type anthropicClient struct {
	apiKey  string
	model   string
	httpCli *http.Client
}

// newAnthropicClient constructs a ready-to-use Anthropic provider.
func newAnthropicClient(apiKey, model string) *anthropicClient {
	return &anthropicClient{
		apiKey: apiKey,
		model:  model,
		httpCli: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// anthropicRequest is the JSON body for POST /v1/messages.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the subset of /v1/messages we care about.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider. It POSTs to the Anthropic Messages API and
// returns the text of the first content block.
func (c *anthropicClient) Complete(ctx context.Context, system, user string) (string, error) {
	body := anthropicRequest{
		Model:     c.model,
		MaxTokens: maxOutputTokens,
		System:    system,
		Messages: []anthropicMessage{
			{Role: "user", Content: user},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: http request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response body: %w", err)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("llm: parse response (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if parsed.Error != nil {
			msg = fmt.Sprintf("HTTP %d: %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
		}
		return "", fmt.Errorf("llm: anthropic API error: %s", msg)
	}

	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("llm: no text content in response (stop_reason=%s)", parsed.StopReason)
}
