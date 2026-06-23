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
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/analytics"
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
	LeadTimeSecs *int64  `json:"leadTimeSecs"` // first_commit_at → merged_at (DORA lead time)
	ReviewSecs   *int64  `json:"reviewSecs"`   // pr.created_at → merged_at (time open / in review)
	MergedAt     string  `json:"mergedAt"`     // the event time the chart plots against
	Title        string  `json:"title,omitempty"`
	Repo         string  `json:"repo,omitempty"`
	ComputedAt   string  `json:"computedAt"`
}

// involvementResponse carries aggregated involvement TEXTURE for one PERSON over
// the requested window (one card per person, never per month×project row).
// Field contract (decisions P2):
//   - featuresShipped: merged PRs authored (observable shipping activity)
//   - reviewsDone:     code reviews given (the invisible senior work)
//   - areasOwned:      distinct repos/areas touched (breadth of ownership)
//   - activeRecently:  true if active in any period within the window
//   - lastActive:      most recent period the person was active (display only)
//   - dimensions:      extensible jsonb with additional texture (no score ever)
//
// There is intentionally NO "score", "rank", "composite", or "total" field.
type involvementResponse struct {
	UserID          string                 `json:"userId"`
	Name            string                 `json:"name,omitempty"`
	Email           string                 `json:"email,omitempty"`
	AvatarURL       string                 `json:"avatarUrl,omitempty"`
	FeaturesShipped int                    `json:"featuresShipped"` // merged PRs authored
	ReviewsDone     int                    `json:"reviewsDone"`     // the invisible work
	AreasOwned      int                    `json:"areasOwned"`      // distinct repos touched
	ActiveRecently  bool                   `json:"activeRecently"`
	LastActive      string                 `json:"lastActive,omitempty"`
	IsAgent         bool                   `json:"isAgent"`
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
	author := r.URL.Query().Get("author")

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
		filter := store.CycleTimeFilter{
			RepoID: repoID,
			From:   from,
			To:     to,
		}
		// Author grouping: a `contributor:<uuid>` token expands to the contributor's
		// full identity set so cycle time filters by ALL their identities (a single
		// login is matched as-is via the same ANY() path). A plain login that maps to
		// a contributor is also expanded so grouping is consistent with the leaderboard.
		if idents, err := expandCycleTimeAuthor(r.Context(), tx, orgID, author); err != nil {
			return err
		} else {
			filter.AuthorIdentities = idents
		}
		var e error
		cts, e = store.ListCycleTimes(r.Context(), tx, orgID, filter)
		return e
	}); err != nil {
		writeMetricsError(w, "list cycle times", err)
		return
	}

	out := make([]cycleTimeResponse, 0, len(cts))
	for _, ct := range cts {
		mergedAt := ""
		if !ct.MergedAt.IsZero() {
			mergedAt = ct.MergedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, cycleTimeResponse{
			ID:           ct.ID,
			PRID:         ct.PRID,
			LeadTimeSecs: ct.LeadTimeSecs,
			ReviewSecs:   ct.ReviewSecs,
			MergedAt:     mergedAt,
			Title:        ct.Title,
			Repo:         ct.Repo,
			ComputedAt:   ct.ComputedAt.UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// expandCycleTimeAuthor resolves the ?author= filter for cycle time into the set
// of lowercased git identities to match PR author_login against. Rules:
//   - "" → nil (no author filter).
//   - "contributor:<uuid>" → the contributor's full identity set (emails+logins).
//   - a plain login/email → if it maps to a contributor, that contributor's full
//     set (so grouping matches the leaderboard); otherwise just the value itself.
//
// Returns nil when a contributor token yields no identities, which would
// otherwise match nothing; callers treat nil as "no filter" only when author was
// empty — here a non-empty author with an empty set still yields an empty result
// because the ANY() match needs at least one value. To keep the chosen-person
// semantics honest we fall back to the literal value so an unmapped login still
// filters to itself.
func expandCycleTimeAuthor(ctx context.Context, tx pgx.Tx, orgID, author string) ([]string, error) {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil, nil
	}
	if cid, ok := analytics.ContributorIDFromAuthor(author); ok {
		idents, err := store.ContributorIdentityValues(ctx, tx, orgID, cid)
		if err != nil {
			return nil, err
		}
		return idents, nil
	}
	// Plain identity: expand to its contributor's full set when mapped.
	lower := strings.ToLower(author)
	cid, err := store.ContributorIDForIdentity(ctx, tx, orgID, lower)
	if err != nil {
		return nil, err
	}
	if cid != "" {
		idents, err := store.ContributorIdentityValues(ctx, tx, orgID, cid)
		if err != nil {
			return nil, err
		}
		if len(idents) > 0 {
			return idents, nil
		}
	}
	return []string{lower}, nil
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

	// Resolve the window. The UI sends a relative token (7d/30d/90d); we also
	// accept an explicit YYYY-MM-DD lower bound for ad-hoc queries. The window
	// start bounds which monthly involvement periods are aggregated into each
	// person's card.
	now := time.Now().UTC()
	windowStart := involvementWindowStart(r.URL.Query().Get("period"), now)

	// Recompute every calendar month the window touches so the texture is fresh.
	// ComputeInvolvement is idempotent (upsert) and self-heals orphan/stale rows.
	for _, m := range monthsInWindow(windowStart, now) {
		if err := h.svc.ComputeInvolvement(r.Context(), orgID, m); err != nil {
			slog.Warn("metrics: compute involvement failed (returning cached data)",
				"org_id", orgID, "period_start", m, "err", err)
			// Non-fatal: return whatever is stored.
		}
	}

	// WithOrg sets the RLS org context — involvement has RLS enabled, so a bare pool
	// would return zero rows under the non-superuser role.
	var members []*store.InvolvementMember
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		members, e = store.ListInvolvementMembers(r.Context(), tx, orgID, windowStart, projectID)
		return e
	}); err != nil {
		writeMetricsError(w, "list involvement", err)
		return
	}

	out := make([]involvementResponse, 0, len(members))
	for _, m := range members {
		resp := involvementResponse{
			UserID:          m.UserID,
			Name:            m.Name,
			Email:           m.Email,
			AvatarURL:       m.AvatarURL,
			FeaturesShipped: m.FeaturesShipped,
			ReviewsDone:     m.ReviewsDone,
			AreasOwned:      m.AreasOwned,
			ActiveRecently:  m.Active,
			IsAgent:         m.IsAgent,
			Dimensions: map[string]interface{}{
				"commitCount":  m.CommitCount,
				"linesAdded":   m.LinesAdded,
				"linesDeleted": m.LinesDeleted,
				"isAgent":      m.IsAgent,
			},
		}
		if !m.LastActive.IsZero() {
			resp.LastActive = m.LastActive.Format("2006-01-02")
		}
		out = append(out, resp)
	}

	writeJSON(w, http.StatusOK, out)
}

// involvementWindowStart maps a period token to the inclusive lower bound on
// involvement period_start. Accepts "7d"/"30d"/"90d" (relative) or an explicit
// "YYYY-MM-DD". Unknown/empty defaults to a 30-day window. The returned bound is
// truncated to the first day of its calendar month, because involvement is
// bucketed monthly — a 30-day window must still include the month a recent
// period started in.
func involvementWindowStart(period string, now time.Time) time.Time {
	days := 30
	switch period {
	case "7d":
		days = 7
	case "30d", "":
		days = 30
	case "90d":
		days = 90
	default:
		if t, err := time.Parse("2006-01-02", period); err == nil {
			return monthStart(t.UTC())
		}
	}
	return monthStart(now.AddDate(0, 0, -days))
}

// monthStart returns the first instant (UTC midnight) of t's calendar month.
func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// monthsInWindow lists the first-of-month timestamps from windowStart's month
// through now's month inclusive, so every period the window aggregates is
// recomputed. Capped defensively at 24 months.
func monthsInWindow(windowStart, now time.Time) []time.Time {
	var out []time.Time
	m := monthStart(windowStart)
	endM := monthStart(now)
	for i := 0; !m.After(endM) && i < 24; i++ {
		out = append(out, m)
		m = m.AddDate(0, 1, 0)
	}
	return out
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
