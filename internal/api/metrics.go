// Package api — metrics.go
// REST handlers for derived metrics: cycle time (DORA), involvement texture,
// and LLM-backed effort estimates.
//
// Decision compliance:
//   - P2: Involvement responses expose per-dimension TEXTURE only (features_shipped,
//     reviews_done, areas_owned, active, dimensions). There is no "score" field,
//     no rank, no composite number in any response type.
//   - P3: Effort estimates link to a PR and record the model that produced them.
//   - A2/S1: All routes require RequireAuth + OrgScope (RLS enforced in DB).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/metrics"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// RegisterMetricsRoutes wires the derived-metrics endpoints onto mux.
// All routes require a valid JWT (RequireAuth) and an active org (OrgScope).
//
// Routes:
//
//	GET  /api/metrics/cycle-time?repo=&from=&to=  → series of cycle-time records
//	GET  /api/metrics/involvement?project=&period= → per-user TEXTURE rows (no score)
//	POST /api/metrics/estimate/{prId}              → trigger LLM estimate → 202 + result
func RegisterMetricsRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	llmSvc := llm.New(cfg)
	svc := metrics.New(database, llmSvc)
	h := &metricsHandlers{db: database, svc: svc}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/metrics/cycle-time", auth(http.HandlerFunc(h.cycleTime)))
	mux.Handle("GET /api/metrics/involvement", auth(http.HandlerFunc(h.involvement)))
	mux.Handle("POST /api/metrics/estimate/{prId}", auth(http.HandlerFunc(h.estimate)))
}

type metricsHandlers struct {
	db  *db.DB
	svc *metrics.Service
}

// ── Response types ─────────────────────────────────────────────────────────────
//
// Involvement response deliberately has NO score/rank field (decisions P2).
// Each field is an independent observable fact from git history.

type cycleTimeResponse struct {
	ID           string  `json:"id"`
	PRID         *string `json:"prId,omitempty"`
	LeadTimeSecs *int64  `json:"leadTimeSecs"`  // first_commit_at → merged_at
	ReviewSecs   *int64  `json:"reviewSecs"`    // pr.created_at → merged_at
	ComputedAt   string  `json:"computedAt"`
}

// involvementResponse carries involvement TEXTURE for one user in one period.
// Field contract (decisions P2):
//   - features_shipped: merged PRs authored (observable shipping activity)
//   - reviews_done:     PRs reviewed / merged by others (invisible senior work)
//   - areas_owned:      distinct repos/areas touched (breadth of ownership)
//   - active:           true if any activity in the period
//   - dimensions:       extensible jsonb with additional texture (no score ever added)
//
// There is intentionally NO "score", "rank", "composite", or "total" field.
type involvementResponse struct {
	ID              string                 `json:"id"`
	UserID          *string                `json:"userId,omitempty"`
	ProjectID       *string                `json:"projectId,omitempty"`
	PeriodStart     string                 `json:"periodStart"`
	FeaturesShipped int                    `json:"featuresShipped"` // merged PRs authored
	ReviewsDone     int                    `json:"reviewsDone"`     // the invisible work
	AreasOwned      int                    `json:"areasOwned"`      // distinct repos touched
	Active          bool                   `json:"active"`
	Dimensions      map[string]interface{} `json:"dimensions"` // extensible texture only
}

type estimateResponse struct {
	ID         string                 `json:"id"`
	PRID       *string                `json:"prId,omitempty"`
	Difficulty float64                `json:"difficulty"`
	Rationale  string                 `json:"rationale"`
	Evidence   map[string]interface{} `json:"evidence"`
	Model      string                 `json:"model"`
	CreatedAt  string                 `json:"createdAt"`
}

// ── GET /api/metrics/cycle-time ──────────────────────────────────────────────
//
// Query params:
//   repo=<repoID>   optional repo filter
//   from=<RFC3339>  optional lower bound on computed_at
//   to=<RFC3339>    optional upper bound on computed_at
//
// Triggers a fresh ComputeCycleTimes when ?repo= is provided, then returns the
// stored series. Without ?repo= returns all stored cycle times for the org.

func (h *metricsHandlers) cycleTime(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	repoID := r.URL.Query().Get("repo")

	var from, to time.Time
	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = t
		}
	}

	// Eagerly recompute when a specific repo is requested so the response is fresh.
	if repoID != "" {
		if err := h.svc.ComputeCycleTimes(r.Context(), orgID, repoID); err != nil {
			slog.Warn("metrics: compute cycle times failed (returning cached data)",
				"org_id", orgID, "repo_id", repoID, "err", err)
			// Non-fatal: we still return whatever is stored.
		}
	}

	// Run inside WithOrg so app.current_org is set — cycle_times has RLS enabled and
	// a bare pool (no org context) returns ZERO rows under the non-superuser role.
	var cts []*store.CycleTime
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		cts, e = store.ListCycleTimes(r.Context(), tx, orgID, store.CycleTimeFilter{
			RepoID: repoID,
			From:   from,
			To:     to,
		})
		return e
	}); err != nil {
		writeMetricsError(w, "list cycle times", err)
		return
	}

	out := make([]cycleTimeResponse, 0, len(cts))
	for _, ct := range cts {
		out = append(out, cycleTimeResponse{
			ID:           ct.ID,
			PRID:         ct.PRID,
			LeadTimeSecs: ct.LeadTimeSecs,
			ReviewSecs:   ct.ReviewSecs,
			ComputedAt:   ct.ComputedAt.UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// ── GET /api/metrics/involvement ─────────────────────────────────────────────
//
// Query params:
//   project=<projectID>       optional project filter
//   period=YYYY-MM-DD         optional period_start filter (exact month start)
//
// Responds with per-user TEXTURE rows — never a score, never a rank.
// The response shape is explicitly documented in involvementResponse above.

func (h *metricsHandlers) involvement(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	projectID := r.URL.Query().Get("project")

	var periodStart time.Time
	if s := r.URL.Query().Get("period"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			periodStart = t.UTC()
		}
	}

	// Trigger a recompute for the requested period when provided so fresh data
	// is always returned. ComputeInvolvement is idempotent (upsert).
	if !periodStart.IsZero() {
		if err := h.svc.ComputeInvolvement(r.Context(), orgID, periodStart); err != nil {
			slog.Warn("metrics: compute involvement failed (returning cached data)",
				"org_id", orgID, "period_start", periodStart, "err", err)
			// Non-fatal: return stored data.
		}
	}

	// WithOrg sets the RLS org context — involvement has RLS enabled, so a bare pool
	// would return zero rows under the non-superuser role.
	var invs []*store.Involvement
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		invs, e = store.ListInvolvement(r.Context(), tx, orgID, store.InvolvementFilter{
			ProjectID:   projectID,
			PeriodStart: periodStart,
		})
		return e
	}); err != nil {
		writeMetricsError(w, "list involvement", err)
		return
	}

	out := make([]involvementResponse, 0, len(invs))
	for _, inv := range invs {
		out = append(out, involvementResponse{
			ID:              inv.ID,
			UserID:          inv.UserID,
			ProjectID:       inv.ProjectID,
			PeriodStart:     inv.PeriodStart.Format("2006-01-02"),
			FeaturesShipped: inv.FeaturesShipped,
			ReviewsDone:     inv.ReviewsDone,
			AreasOwned:      inv.AreasOwned,
			Active:          inv.Active,
			Dimensions:      inv.Dimensions,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// ── POST /api/metrics/estimate/{prId} ────────────────────────────────────────
//
// Triggers an LLM difficulty estimate for the given PR. The caller must supply
// the git diff in the request body JSON:
//
//	{ "diff": "<git diff text>" }
//
// Returns 202 with the estimate result. Returns 503 when LLM is not configured.

type estimateRequest struct {
	Diff string `json:"diff"`
}

func (h *metricsHandlers) estimate(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	prID := r.PathValue("prId")
	if prID == "" {
		writeError(w, http.StatusBadRequest, "prId path parameter is required")
		return
	}

	var req estimateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Diff == "" {
		writeError(w, http.StatusBadRequest, "diff is required")
		return
	}

	est, err := h.svc.EstimateForPR(r.Context(), orgID, prID, req.Diff)
	if err != nil {
		if errors.Is(err, llm.ErrLLMNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "LLM not configured")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "PR not found")
			return
		}
		writeMetricsError(w, "estimate for PR", err)
		return
	}

	out := estimateResponse{
		ID:         est.ID,
		PRID:       est.PRID,
		Difficulty: est.Difficulty,
		Rationale:  est.Rationale,
		Evidence:   est.Evidence,
		Model:      est.Model,
		CreatedAt:  est.CreatedAt.UTC().Format(time.RFC3339),
	}

	// 202 Accepted — compute was triggered and result is ready in the same call.
	writeJSON(w, http.StatusAccepted, out)
}

// ── Error helper ──────────────────────────────────────────────────────────────

func writeMetricsError(w http.ResponseWriter, msg string, err error) {
	slog.Error("metrics api error", "msg", msg, "err", err)
	writeError(w, http.StatusInternalServerError, msg)
}
