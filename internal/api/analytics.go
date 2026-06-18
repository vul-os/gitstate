// Package api — analytics.go
// REST handlers for the git-analytics dashboard (gitrack-level): totals &
// averages, the contribution heatmap, commits-over-time, the contributor
// leaderboard, the per-repo table, and the per-day commit drill-down.
//
// All routes require a valid JWT (RequireAuth) and an active org (OrgScope);
// every read runs inside db.WithOrg so RLS enforces org isolation (A2/S1).
// Filters (?from=&to=&repo=&author=) are parsed and defaulted by the analytics
// Service (default range = last 9 months) and passed as bind parameters.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/exo/gitstate/internal/analytics"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
)

// RegisterAnalyticsRoutes wires the git-analytics endpoints onto mux.
//
// Routes (all behind RequireAuth + OrgScope, all accept ?from=&to=&repo=&author=):
//
//	GET /api/analytics/summary             → totals + averages
//	GET /api/analytics/heatmap             → []{date, count} per calendar day
//	GET /api/analytics/commits-over-time   → []{date, count} (?bucket=day|week)
//	GET /api/analytics/contributors        → leaderboard
//	GET /api/analytics/repos               → per-repo table
//	GET /api/analytics/day/{date}          → drill-down: commits on YYYY-MM-DD
func RegisterAnalyticsRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	svc := analytics.New(database)
	h := &analyticsHandlers{svc: svc}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/analytics/summary", auth(http.HandlerFunc(h.summary)))
	mux.Handle("GET /api/analytics/heatmap", auth(http.HandlerFunc(h.heatmap)))
	mux.Handle("GET /api/analytics/commits-over-time", auth(http.HandlerFunc(h.commitsOverTime)))
	mux.Handle("GET /api/analytics/contributors", auth(http.HandlerFunc(h.contributors)))
	mux.Handle("GET /api/analytics/repos", auth(http.HandlerFunc(h.repos)))
	mux.Handle("GET /api/analytics/day/{date}", auth(http.HandlerFunc(h.day)))
}

type analyticsHandlers struct {
	svc *analytics.Service
}

// parseFilter pulls ?from=&to=&repo=&author= from the request and defaults the
// window to the last 9 months. Returns false (after writing a 400) on bad input.
func (h *analyticsHandlers) parseFilter(w http.ResponseWriter, r *http.Request) (analytics.Filter, bool) {
	q := r.URL.Query()
	f, err := analytics.ParseFilter(analytics.FilterInput{
		From:   q.Get("from"),
		To:     q.Get("to"),
		RepoID: q.Get("repo"),
		Author: q.Get("author"),
	}, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return analytics.Filter{}, false
	}
	return f, true
}

func writeAnalyticsError(w http.ResponseWriter, msg string, err error) {
	slog.Error("analytics api error", "msg", msg, "err", err)
	writeError(w, http.StatusInternalServerError, msg)
}

// GET /api/analytics/summary
func (h *analyticsHandlers) summary(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Summary(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute summary", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/analytics/heatmap
func (h *analyticsHandlers) heatmap(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Heatmap(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute heatmap", err)
		return
	}
	writeJSON(w, http.StatusOK, emptySlice(res))
}

// GET /api/analytics/commits-over-time?bucket=day|week
func (h *analyticsHandlers) commitsOverTime(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.CommitsOverTime(r.Context(), orgID, f, r.URL.Query().Get("bucket"))
	if err != nil {
		writeAnalyticsError(w, "compute commits-over-time", err)
		return
	}
	writeJSON(w, http.StatusOK, emptySlice(res))
}

// GET /api/analytics/contributors
func (h *analyticsHandlers) contributors(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Contributors(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute contributors", err)
		return
	}
	writeJSON(w, http.StatusOK, emptySlice(res))
}

// GET /api/analytics/repos
func (h *analyticsHandlers) repos(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.RepoStats(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute repo stats", err)
		return
	}
	writeJSON(w, http.StatusOK, emptySlice(res))
}

// GET /api/analytics/day/{date}
func (h *analyticsHandlers) day(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	day, err := analytics.ParseDay(r.PathValue("date"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.CommitsOnDay(r.Context(), orgID, f, day)
	if err != nil {
		writeAnalyticsError(w, "compute day drill-down", err)
		return
	}
	writeJSON(w, http.StatusOK, emptySlice(res))
}

// emptySlice ensures a nil slice serialises as [] rather than null, so the
// frontend can always iterate the response.
func emptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
