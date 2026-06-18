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
	Key            string  `json:"key"`
	Name           string  `json:"name"`
	PerBuilderUSD  int     `json:"perBuilderUsd"`  // per_builder_cents / 100
	IncludedLLMUSD int     `json:"includedLlmUsd"` // included_llm_cents / 100
	OverageMarkup  float64 `json:"overageMarkup"`
	Builders       int     `json:"builders"` // cap: 0 = unlimited
}

// RegisterPublicPlans wires GET /api/plans (public — for the pricing page).
func RegisterPublicPlans(mux *http.ServeMux, database *db.DB, _ *config.Config) {
	mux.HandleFunc("GET /api/plans", func(w http.ResponseWriter, r *http.Request) {
		plans, err := store.ListPlans(r.Context(), database.Pool())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load plans")
			return
		}
		out := make([]publicPlan, 0, len(plans))
		for _, p := range plans {
			out = append(out, publicPlan{
				Key:            p.Key,
				Name:           p.Name,
				PerBuilderUSD:  p.PerBuilderCents / 100,
				IncludedLLMUSD: p.IncludedLLMCents / 100,
				OverageMarkup:  p.OverageMarkup,
				Builders:       p.Builders,
			})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
