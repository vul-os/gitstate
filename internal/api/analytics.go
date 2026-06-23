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
	"strings"
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
//	GET /api/analytics/pull-requests       → PR totals, merge-rate, lead-time, throughput
//	GET /api/analytics/issue-flow          → issue state breakdown + opened/closed series + by-project
//	GET /api/analytics/agent-share         → agent vs human commit split (+ over time)
//	GET /api/analytics/projects            → per-project table
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
	mux.Handle("GET /api/analytics/pull-requests", auth(http.HandlerFunc(h.pullRequests)))
	mux.Handle("GET /api/analytics/issue-flow", auth(http.HandlerFunc(h.issueFlow)))
	mux.Handle("GET /api/analytics/agent-share", auth(http.HandlerFunc(h.agentShare)))
	mux.Handle("GET /api/analytics/projects", auth(http.HandlerFunc(h.projects)))
	mux.Handle("GET /api/analytics/day/{date}", auth(http.HandlerFunc(h.day)))
}

type analyticsHandlers struct {
	svc *analytics.Service
}

// parseFilter pulls ?from=&to=&repo=&author= from the request and defaults the
// window to the last 9 months. Returns false (after writing a 400) on bad input.
func (h *analyticsHandlers) parseFilter(w http.ResponseWriter, r *http.Request) (analytics.Filter, bool) {
	q := r.URL.Query()
	// repo filters on repos.id (a UUID). Older clients sent a full_name here, which
	// Postgres rejects when bound to a uuid column (22P02) and 500s every panel.
	// Guard: ignore a non-UUID repo value rather than crash (treat as "all repos").
	repoID := q.Get("repo")
	// A repo UUID is 36 chars with no slash; a full_name ("owner/name") has a slash.
	// Ignore anything that isn't UUID-shaped so a stray value can't 22P02 the panels.
	if repoID != "" && (len(repoID) != 36 || strings.Contains(repoID, "/")) {
		repoID = ""
	}
	f, err := analytics.ParseFilter(analytics.FilterInput{
		From:   q.Get("from"),
		To:     q.Get("to"),
		RepoID: repoID,
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

// GET /api/analytics/pull-requests
func (h *analyticsHandlers) pullRequests(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.PullRequests(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute pull-requests", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/analytics/issue-flow
func (h *analyticsHandlers) issueFlow(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.IssueFlow(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute issue-flow", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/analytics/agent-share
func (h *analyticsHandlers) agentShare(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.AgentShare(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute agent-share", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/analytics/projects
func (h *analyticsHandlers) projects(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Projects(r.Context(), orgID, f)
	if err != nil {
		writeAnalyticsError(w, "compute projects", err)
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
