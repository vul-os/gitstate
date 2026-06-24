package api

import (
	"net/http"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/docs"
	"github.com/exo/gitstate/internal/store"
)

// RegisterDocsRoutes wires the public documentation API (no auth, no DB —
// docs are embedded markdown rendered by the frontend).
func RegisterDocsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, _ *http.Request) {
		list, err := docs.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load docs")
			return
		}
		writeJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /api/docs/{slug}", func(w http.ResponseWriter, r *http.Request) {
		d, ok, err := docs.Get(r.PathValue("slug"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load doc")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "doc not found")
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
}

// publicPlan is the pricing-page shape (per-builder tier model; prices defined in
// USD, the backend charges in ZAR at capture-time FX). The web pricing page depends
// on this exact JSON shape.
type publicPlan struct {
	Key string `json:"key"`
	Name string `json:"name"`
	// PerBuilderUSD is per_builder_cents/100, or null for enterprise (custom
	// pricing) so the pricing page renders "Custom" rather than "$0/builder".
	PerBuilderUSD  *float64 `json:"perBuilderUsd"`
	IncludedLLMUSD int      `json:"includedLlmUsd"` // included_llm_cents / 100
	// BYOKPerBuilderUSD is the per-builder price when bringing your own LLM key:
	// the base minus the included-LLM value (you don't pay for managed AI you don't
	// use). null for free (already $0/BYOK) and enterprise (custom).
	BYOKPerBuilderUSD *float64 `json:"byokPerBuilderUsd"`
	OverageMarkup     float64  `json:"overageMarkup"`
	Builders          int      `json:"builders"` // cap: 0 = unlimited
	Features          map[string]any `json:"features"`
}

// RegisterPublicPlans wires GET /api/plans (public — for the pricing page).
// It also folds in the public model catalog (GET /api/models) so the catalog
// rides an already-registered registrar without touching router.go.
func RegisterPublicPlans(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	RegisterModelRoutes(mux, cfg)
	mux.HandleFunc("GET /api/plans", func(w http.ResponseWriter, r *http.Request) {
		plans, err := store.ListPlans(r.Context(), database.Pool())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load plans")
			return
		}
		// Only the canonical per-builder ladder is public, in ladder order;
		// legacy rows stay in the table for back-compat but hidden.
		byKey := make(map[string]store.Plan, len(plans))
		for _, p := range plans {
			byKey[p.Key] = p
		}
		out := make([]publicPlan, 0, 5)
		for _, key := range []string{"free", "starter", "pro", "scale", "enterprise"} {
			p, ok := byKey[key]
			if !ok {
				continue
			}
			// Enterprise has custom pricing → null per-builder price so the
			// frontend shows "Custom" instead of "$0/builder". Whatever the
			// seed/store holds for enterprise is overridden to null here at the
			// API boundary; priced tiers keep their numbers.
			var perBuilder, byok *float64
			if key != "enterprise" {
				v := float64(p.PerBuilderCents) / 100
				perBuilder = &v
				// BYOK price = base − included-LLM value (don't charge for managed AI
				// the customer isn't using). Free stays $0; never below 0.
				if p.PerBuilderCents > 0 {
					b := float64(p.PerBuilderCents-p.IncludedLLMCents) / 100
					if b < 0 {
						b = 0
					}
					byok = &b
				}
			}
			out = append(out, publicPlan{
				Key:               p.Key,
				Name:              p.Name,
				PerBuilderUSD:     perBuilder,
				IncludedLLMUSD:    p.IncludedLLMCents / 100,
				BYOKPerBuilderUSD: byok,
				OverageMarkup:     p.OverageMarkup,
				Features:          p.Features,
				Builders:          p.Builders,
			})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
