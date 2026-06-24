package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
)

// ─── Model catalog ─────────────────────────────────────────────────────────
//
// Models(cfg) returns the public, marked-up multi-provider model catalog
// restricted to the three providers gitstate resells: Anthropic, OpenAI, and
// Google. Base per-MTok prices come from the in-process llmux pricing catalog
// (GET {baseURL}/v1/catalog.json) when the gateway is up, filtered to those
// three providers; offline, a curated flagship + mini/haiku/flash fallback is
// used so the endpoint always works. Our on-charge price is base × cfg.LLM.Markup
// (default 1.05 = +5%), marked explicitly via the OurInput/OurOutput fields.

// allowedProviders is the resale allowlist. Anything else from the gateway
// catalog (deepseek, mistral, …) is dropped.
var allowedProviders = map[string]bool{
	"anthropic": true,
	"openai":    true,
	"google":    true,
}

// ModelInfo is one catalog entry: base provider price plus our marked-up price.
type ModelInfo struct {
	Provider      string `json:"provider"` // "anthropic" | "openai" | "google"
	ID            string `json:"id"`
	DisplayName   string `json:"displayName"`
	ContextTokens int    `json:"contextTokens"`

	// Base prices, USD per million tokens (the upstream provider price).
	InputUSDPerMTok  float64 `json:"inputUsdPerMTok"`
	OutputUSDPerMTok float64 `json:"outputUsdPerMTok"`

	// Our on-charged prices = base × markup. Marked explicitly so the client
	// knows these already include the gitstate markup.
	OurInputUSDPerMTok  float64 `json:"ourInputUsdPerMTok"`
	OurOutputUSDPerMTok float64 `json:"ourOutputUsdPerMTok"`

	// Markup is the multiplier applied (e.g. 1.05), surfaced for transparency.
	Markup float64 `json:"markup"`
}

// Models returns the resale catalog for the three allowed providers with the
// configured markup applied. It sources base prices from the running gateway's
// catalog.json when available, else the curated fallback. cfg.LLM.Markup
// (default 1.05) sets the on-charge multiplier.
//
// gw may be nil — pass nil to force the curated fallback (e.g. tests, or
// gateway-off boots).
func Models(cfg *config.Config, gw *Gateway) []ModelInfo {
	markup := 1.05
	if cfg != nil && cfg.LLM.Markup > 0 {
		markup = cfg.LLM.Markup
	}

	base := fetchGatewayCatalog(gw)
	if len(base) == 0 {
		base = curatedFallback()
	}

	out := make([]ModelInfo, 0, len(base))
	for _, m := range base {
		if !allowedProviders[m.Provider] {
			continue
		}
		m.Markup = markup
		m.OurInputUSDPerMTok = round4(m.InputUSDPerMTok * markup)
		m.OurOutputUSDPerMTok = round4(m.OutputUSDPerMTok * markup)
		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ─── Gateway catalog.json ──────────────────────────────────────────────────

// gatewayCatalog mirrors llmux's GET /v1/catalog.json response shape.
type gatewayCatalog struct {
	Prices map[string]gatewayPrice `json:"prices"`
}

// gatewayPrice mirrors llmux's pricing.Price JSON.
type gatewayPrice struct {
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	InputPerMTok  float64 `json:"input_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
	ContextWindow int     `json:"context_window"`
}

// fetchGatewayCatalog GETs {baseURL}/catalog.json from the running gateway and
// maps the three allowed providers into ModelInfo entries (base prices only).
// Returns nil when the gateway is off or unreachable, so the caller falls back
// to the curated list.
func fetchGatewayCatalog(gw *Gateway) []ModelInfo {
	if !gw.Enabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.BaseURL()+"/catalog.json", nil)
	if err != nil {
		return nil
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	var cat gatewayCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil
	}

	out := make([]ModelInfo, 0, len(cat.Prices))
	for _, p := range cat.Prices {
		if !allowedProviders[p.Provider] {
			continue
		}
		id := p.Model
		// Catalog keys are often "provider/model"; expose the bare model ID.
		if i := strings.IndexByte(id, '/'); i >= 0 {
			id = id[i+1:]
		}
		out = append(out, ModelInfo{
			Provider:         p.Provider,
			ID:               id,
			DisplayName:      displayName(p.Provider, id),
			ContextTokens:    p.ContextWindow,
			InputUSDPerMTok:  p.InputPerMTok,
			OutputUSDPerMTok: p.OutputPerMTok,
		})
	}
	return out
}

// ─── Curated offline fallback ──────────────────────────────────────────────

// curatedFallback is the offline catalog: current flagship + mini/haiku/flash
// models for each allowed provider, with public per-MTok prices. Anthropic
// prices/IDs are the current generation (Opus 4.8 / Sonnet 4.6 / Haiku 4.5).
func curatedFallback() []ModelInfo {
	return []ModelInfo{
		// Anthropic
		{Provider: "anthropic", ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", ContextTokens: 1_000_000, InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
		{Provider: "anthropic", ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", ContextTokens: 1_000_000, InputUSDPerMTok: 3, OutputUSDPerMTok: 15},
		{Provider: "anthropic", ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", ContextTokens: 200_000, InputUSDPerMTok: 1, OutputUSDPerMTok: 5},

		// OpenAI
		{Provider: "openai", ID: "gpt-4o", DisplayName: "GPT-4o", ContextTokens: 128_000, InputUSDPerMTok: 2.5, OutputUSDPerMTok: 10},
		{Provider: "openai", ID: "gpt-4o-mini", DisplayName: "GPT-4o mini", ContextTokens: 128_000, InputUSDPerMTok: 0.15, OutputUSDPerMTok: 0.6},

		// Google
		{Provider: "google", ID: "gemini-1.5-pro", DisplayName: "Gemini 1.5 Pro", ContextTokens: 2_000_000, InputUSDPerMTok: 1.25, OutputUSDPerMTok: 5},
		{Provider: "google", ID: "gemini-1.5-flash", DisplayName: "Gemini 1.5 Flash", ContextTokens: 1_000_000, InputUSDPerMTok: 0.075, OutputUSDPerMTok: 0.3},
	}
}

// displayName derives a human label for a gateway-sourced model id when the
// catalog doesn't carry one.
func displayName(provider, id string) string {
	switch provider {
	case "anthropic":
		return prettyClaude(id)
	case "openai":
		return strings.ToUpper(strings.ReplaceAll(id, "-", " "))
	case "google":
		return titleWords(id)
	default:
		return id
	}
}

func prettyClaude(id string) string { return titleWords(id) }

// titleWords upper-cases the first letter of each hyphen/space-separated word
// in an ASCII model id (avoids the deprecated strings.Title).
func titleWords(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == ' ' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// round4 rounds to 4 decimal places so marked-up prices stay tidy in JSON.
func round4(v float64) float64 {
	return float64(int64(v*1e4+0.5)) / 1e4
}
