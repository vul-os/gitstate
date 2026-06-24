package api

import (
	"net/http"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/llm"
)

// modelGateway holds the optional running llmux gateway, injected by main.go via
// SetModelGateway BEFORE NewRouter. When nil, the model catalog falls back to
// llm's curated offline list. Single-writer-at-startup, read-only thereafter.
var modelGateway *llm.Gateway

// SetModelGateway injects the in-process LLM gateway used to source live model
// prices for GET /api/models. Call it before api.NewRouter. Safe to pass nil
// (gateway off) — the catalog then uses the curated fallback.
func SetModelGateway(gw *llm.Gateway) { modelGateway = gw }

// RegisterModelRoutes wires GET /api/models — the public multi-provider model
// catalog (Anthropic/OpenAI/Google only) with our +5% markup applied. Folded
// into RegisterPublicPlans so it rides an already-registered registrar (no
// router.go change). Exposed for direct wiring/testing too.
func RegisterModelRoutes(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, llm.Models(cfg, modelGateway))
	})
}
