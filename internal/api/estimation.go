// Package api — estimation.go
// Read endpoints for the self-calibrating effort estimator (migration 017):
//
//	GET /api/estimation/accuracy     → per-cohort MAE / bias_ratio (how far off
//	                                    estimates run, e.g. "20% low on payments")
//	GET /api/estimation/calibration  → the learned difficulty→time curve cells
//
// Both are org-scoped: RequireAuth + OrgScope set the JWT identity and RLS org
// context, and every read additionally runs inside db.WithOrg so the
// non-superuser FORCE-RLS role sees app.current_org.
package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// RegisterEstimationRoutes wires the read-only estimation endpoints onto mux.
// The orchestrator calls this from router.go (same pattern as the other
// RegisterXRoutes functions).
func RegisterEstimationRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &estimationHandlers{db: database}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/estimation/accuracy", auth(http.HandlerFunc(h.accuracy)))
	mux.Handle("GET /api/estimation/calibration", auth(http.HandlerFunc(h.calibration)))
}

type estimationHandlers struct {
	db *db.DB
}

// ── Response types ─────────────────────────────────────────────────────────────

type accuracyResponse struct {
	CohortKey string  `json:"cohortKey"`
	N         int     `json:"n"`
	MAESecs   int64   `json:"maeSecs"`   // mean absolute error in seconds
	BiasRatio float64 `json:"biasRatio"` // mean(predicted/actual): <1 ⇒ under-estimating
	// UnderPct is a UI-friendly derived figure: how many percent estimates run
	// LOW (positive) or HIGH (negative). E.g. biasRatio 0.8 ⇒ underPct 20.
	UnderPct  float64 `json:"underPct"`
	UpdatedAt string  `json:"updatedAt"`
}

type calibrationCellResponse struct {
	CohortKey        string `json:"cohortKey"`
	DifficultyBucket int    `json:"difficultyBucket"`
	MedianSecs       int64  `json:"medianSecs"`
	P25Secs          int64  `json:"p25Secs"`
	P75Secs          int64  `json:"p75Secs"`
	MeanSecs         int64  `json:"meanSecs"`
	N                int    `json:"n"`
	UpdatedAt        string `json:"updatedAt"`
}

// ── GET /api/estimation/accuracy ────────────────────────────────────────────────

func (h *estimationHandlers) accuracy(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var rows []store.AccuracyRow
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		rows, e = store.ListAccuracy(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "list accuracy")
		return
	}

	out := make([]accuracyResponse, 0, len(rows))
	for _, a := range rows {
		underPct := 0.0
		if a.BiasRatio > 0 {
			underPct = (1 - a.BiasRatio) * 100
		}
		out = append(out, accuracyResponse{
			CohortKey: a.CohortKey,
			N:         a.N,
			MAESecs:   a.MAESecs,
			BiasRatio: a.BiasRatio,
			UnderPct:  underPct,
			UpdatedAt: a.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /api/estimation/calibration ─────────────────────────────────────────────

func (h *estimationHandlers) calibration(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var cells []store.CalibrationCell
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		cells, e = store.ListCalibration(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "list calibration")
		return
	}

	out := make([]calibrationCellResponse, 0, len(cells))
	for _, c := range cells {
		out = append(out, calibrationCellResponse{
			CohortKey:        c.CohortKey,
			DifficultyBucket: c.DifficultyBucket,
			MedianSecs:       c.MedianSecs,
			P25Secs:          c.P25Secs,
			P75Secs:          c.P75Secs,
			MeanSecs:         c.MeanSecs,
			N:                c.N,
			UpdatedAt:        c.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
