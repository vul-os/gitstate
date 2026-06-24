package llm

import (
	"context"
	"fmt"

	"github.com/exo/gitstate/internal/config"
	"github.com/llmux/llmux/sdks/go/llmux"
)

// GatewayKind is the config value (LLM_GATEWAY / cfg.LLM.Gateway) that enables
// the in-process llmux multi-provider gateway.
const GatewayKind = "llmux"

// Gateway is a handle to the in-process llmux LLM gateway. When the gateway is
// off (cfg.LLM.Gateway != "llmux"), StartGateway returns a nil *Gateway, and
// callers fall back to the existing Anthropic-direct client. A nil *Gateway is
// safe to call BaseURL()/Close() on (they no-op), so callers don't need nil
// checks before deferring Close.
type Gateway struct {
	local   *llmux.Local
	baseURL string // OpenAI-compatible base URL (…/v1)
}

// StartGateway boots the embedded llmux gateway when cfg.LLM.Gateway == "llmux".
// llmux auto-detects provider keys from the environment (ANTHROPIC_API_KEY,
// OPENAI_API_KEY, GEMINI_API_KEY) — already present in config/.env. When the
// gateway is off/unset, it returns (nil, nil): callers treat a nil Gateway as
// "use the legacy Anthropic-direct client".
//
// The caller owns the returned Gateway's lifecycle and must Close() it on
// shutdown.
func StartGateway(ctx context.Context, cfg *config.Config) (*Gateway, error) {
	if cfg == nil || cfg.LLM.Gateway != GatewayKind {
		return nil, nil // gateway off — legacy Anthropic-direct path
	}

	local, err := llmux.Start(llmux.Options{})
	if err != nil {
		return nil, fmt.Errorf("llm: start llmux gateway: %w", err)
	}
	return &Gateway{
		local:   local,
		baseURL: local.OpenAIBaseURL(),
	}, nil
}

// BaseURL returns the gateway's OpenAI-compatible base URL (…/v1), or "" when
// the gateway is off (nil receiver).
func (g *Gateway) BaseURL() string {
	if g == nil {
		return ""
	}
	return g.baseURL
}

// Enabled reports whether a live gateway is backing this handle.
func (g *Gateway) Enabled() bool { return g != nil && g.local != nil }

// Close shuts the embedded gateway down and waits for it to stop. Safe to call
// on a nil *Gateway (no-op).
func (g *Gateway) Close() {
	if g == nil || g.local == nil {
		return
	}
	g.local.Close()
}
