// Package api — contribution.go
// REST handlers for the "dev contribution to outcomes" engine: a multi-dimensional,
// evidence-backed, gaming-resistant view used to *inform* (never auto-decide)
// share allocation in an employee-owned company.
//
// Every route is behind RequireAuth + OrgScope; reads run inside db.WithOrg so
// RLS enforces the org boundary (A2/S1). The composite is a weighted sum of five
// within-project-normalized 0–100 dimension scores; agent-bot identities are
// flagged so they never silently inflate a human (decisions P2/P5). Each member
// object always carries the RAW facts behind every dimension, and the per-member
// drill-down adds the real evidence rows — texture is never a hidden rank (P2).
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/contribution"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/metrics"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterContributionRoutes wires the contribution endpoints onto mux.
//
// Routes (all behind RequireAuth + OrgScope):
//
//	GET /api/contribution?from=&to=          → period, weights, scored members (composite desc)
//	GET /api/contribution/{userId}?from=&to= → one member + evidence drill-down
//	GET /api/contribution/weights            → the org's dimension weights
//	PUT /api/contribution/weights            → set weights (owner/admin only)
//
// Default period = last 90 days when from/to are omitted.
func RegisterContributionRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	svc := contribution.New(database)
	// The metrics service backfills the `involvement` table (the source of the
	// shipped / review / ownership dimensions). On synced data nothing else
	// computes involvement across history, so the contribution window does it
	// on-demand (idempotent upsert) before scoring — see ensureInvolvement.
	// llm is nil here: ComputeInvolvement never touches the LLM.
	metricsSvc := metrics.New(database, llm.New(cfg))
	h := &contributionHandlers{db: database, svc: svc, metrics: metricsSvc}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	// NOTE: register the more specific /weights routes before the {userId}
	// wildcard so they aren't shadowed. (Go 1.22 mux prefers the more specific
	// pattern regardless, but ordering keeps intent clear.)
	mux.Handle("GET /api/contribution/weights", auth(http.HandlerFunc(h.getWeights)))
	mux.Handle("PUT /api/contribution/weights", auth(http.HandlerFunc(h.putWeights)))
	mux.Handle("GET /api/contribution/trends", auth(http.HandlerFunc(h.trends)))
	mux.Handle("GET /api/contribution/{userId}", auth(http.HandlerFunc(h.member)))
	mux.Handle("GET /api/contribution", auth(http.HandlerFunc(h.report)))

	// Peer kudos — same auth chain, same Service.
	mux.Handle("GET /api/kudos", auth(http.HandlerFunc(h.listKudos)))
	mux.Handle("POST /api/kudos", auth(http.HandlerFunc(h.postKudos)))
}

type contributionHandlers struct {
	db      *db.DB
	svc     *contribution.Service
	metrics *metrics.Service
}

// maxInvolvementMonths caps the per-request month backfill loop. Seven years of
// history (the real data starts 2019) is ~85 months; 120 leaves generous head-
// room while preventing a pathological window (e.g. a bad from=year-0001) from
// computing thousands of months in one request.
const maxInvolvementMonths = 120

// ensureInvolvement computes per-month involvement texture for every calendar
// month the [from,to] window touches, so the shipped / review / ownership
// dimensions are populated for synced data (which otherwise only has the ~2
// months produced on-demand by the Involvement page).
//
// ComputeInvolvement is idempotent (ReplaceUserInvolvement upserts and self-
// heals), so re-running a month is cheap and safe. Failures are logged and
// swallowed: a stale/partial month must never 500 the whole report — the
// reporting layer simply reads whatever is stored.
func (h *contributionHandlers) ensureInvolvement(ctx context.Context, orgID string, p contribution.Period) {
	if h.metrics == nil {
		return
	}
	// Clamp the loop START to the org's earliest activity within the window. An
	// "All time" window has from≈2000-01, but real git history starts much later
	// (e.g. 2019). Without this clamp the 120-month budget is spent on empty
	// pre-history months and never reaches the actual data. We anchor on the
	// earliest commit/PR so the budget covers REAL months.
	from := p.From
	if earliest, ok := h.earliestActivity(ctx, orgID, p.From, p.To); ok && earliest.After(from) {
		from = earliest
	}

	// First-of-month for the (clamped) window start through the window end.
	m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	endMonth := time.Date(p.To.Year(), p.To.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; !m.After(endMonth) && i < maxInvolvementMonths; i++ {
		if err := h.metrics.ComputeInvolvement(ctx, orgID, m); err != nil {
			slog.Warn("contribution: compute involvement failed (using stored data)",
				"org_id", orgID, "period_start", m.Format("2006-01-02"), "err", err)
		}
		m = m.AddDate(0, 1, 0)
	}
}

// earliestActivity returns the org's earliest commit/PR timestamp within [from,to],
// used to anchor the involvement backfill loop on real history (so the all-time
// floor doesn't waste the month budget on empty pre-history). ok=false when there
// is no activity (then the caller keeps the original window start).
func (h *contributionHandlers) earliestActivity(ctx context.Context, orgID string, from, to time.Time) (time.Time, bool) {
	var earliest *time.Time
	err := h.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			SELECT min(t) FROM (
				SELECT min(c.committed_at) AS t FROM commits c
				WHERE c.org_id = $1 AND c.committed_at >= $2 AND c.committed_at < $3
				UNION ALL
				SELECT min(p.merged_at) AS t FROM pull_requests p
				WHERE p.org_id = $1 AND p.merged_at >= $2 AND p.merged_at < $3
			) s`
		return tx.QueryRow(ctx, q, orgID, from, to).Scan(&earliest)
	})
	if err != nil || earliest == nil {
		return time.Time{}, false
	}
	return earliest.UTC(), true
}

// parsePeriod pulls ?from=&to= (RFC3339 or YYYY-MM-DD), defaulting to last 90d.
func (h *contributionHandlers) parsePeriod(w http.ResponseWriter, r *http.Request) (contribution.Period, bool) {
	q := r.URL.Query()
	var from, to time.Time
	if s := q.Get("from"); s != "" {
		t, err := parseContribDate(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'from' date: "+err.Error())
			return contribution.Period{}, false
		}
		from = t
	}
	if s := q.Get("to"); s != "" {
		t, err := parseContribDate(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'to' date: "+err.Error())
			return contribution.Period{}, false
		}
		to = t
	}
	return contribution.ResolvePeriod(from, to, time.Now().UTC()), true
}

func parseContribDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errors.New("expected RFC3339 or YYYY-MM-DD")
}

// ── JSON shapes (the exact API contract) ────────────────────────────────────────

type dimDetailJSON struct {
	Score float64 `json:"score"`
	Raw   any     `json:"raw"`
}

type shippedRawJSON struct {
	MergedPRs       int `json:"mergedPRs"`
	IssuesClosed    int `json:"issuesClosed"`
	FeaturesShipped int `json:"featuresShipped"`
}
type reviewRawJSON struct {
	ReviewsDone int `json:"reviewsDone"`
}
type effortRawJSON struct {
	EffortPoints float64 `json:"effortPoints"`
}
type qualityRawJSON struct {
	Reverts        int     `json:"reverts"`
	AvgCycleHours  float64 `json:"avgCycleHours"`
	BugsIntroduced int     `json:"bugsIntroduced"`
	TestCoupling   float64 `json:"testCoupling"`
}
type ownershipRawJSON struct {
	AreasOwned int `json:"areasOwned"`
}
type durabilityRawJSON struct {
	SurvivingLines int     `json:"survivingLines"`
	AuthoredLines  int     `json:"authoredLines"`
	SurvivalPct    float64 `json:"survivalPct"`
}

type dimensionsJSON struct {
	Shipped    dimDetailJSON `json:"shipped"`
	Review     dimDetailJSON `json:"review"`
	Effort     dimDetailJSON `json:"effort"`
	Quality    dimDetailJSON `json:"quality"`
	Ownership  dimDetailJSON `json:"ownership"`
	Durability dimDetailJSON `json:"durability"`
}

type authorshipJSON struct {
	HumanCommits int     `json:"humanCommits"`
	AgentCommits int     `json:"agentCommits"`
	AgentPct     float64 `json:"agentPct"`
}

type memberJSON struct {
	UserID     string         `json:"userId"`
	Name       string         `json:"name"`
	Email      string         `json:"email"`
	IsAgentBot bool           `json:"isAgentBot"`
	Composite  float64        `json:"composite"`
	Dimensions dimensionsJSON `json:"dimensions"`
	Authorship authorshipJSON `json:"authorship"`
}

type weightsJSON struct {
	Shipped    float64 `json:"shipped"`
	Review     float64 `json:"review"`
	Effort     float64 `json:"effort"`
	Quality    float64 `json:"quality"`
	Ownership  float64 `json:"ownership"`
	Durability float64 `json:"durability"`
}

func toMemberJSON(m contribution.Member) memberJSON {
	return memberJSON{
		UserID:     m.UserID,
		Name:       m.Name,
		Email:      m.Email,
		IsAgentBot: m.IsAgentBot,
		Composite:  m.Composite,
		Dimensions: dimensionsJSON{
			Shipped: dimDetailJSON{Score: m.Dimensions.Shipped, Raw: shippedRawJSON{
				MergedPRs: m.Raw.MergedPRs, IssuesClosed: m.Raw.IssuesClosed, FeaturesShipped: m.Raw.FeaturesShipped,
			}},
			Review: dimDetailJSON{Score: m.Dimensions.Review, Raw: reviewRawJSON{ReviewsDone: m.Raw.ReviewsDone}},
			Effort: dimDetailJSON{Score: m.Dimensions.Effort, Raw: effortRawJSON{EffortPoints: m.Raw.EffortPoints}},
			Quality: dimDetailJSON{Score: m.Dimensions.Quality, Raw: qualityRawJSON{
				Reverts: m.Raw.Reverts, AvgCycleHours: round1(m.Raw.AvgCycleHours),
				BugsIntroduced: m.Raw.BugsIntroduced, TestCoupling: round2(m.Raw.TestCoupling()),
			}},
			Ownership: dimDetailJSON{Score: m.Dimensions.Ownership, Raw: ownershipRawJSON{AreasOwned: m.Raw.AreasOwned}},
			Durability: dimDetailJSON{Score: m.Dimensions.Durability, Raw: durabilityRawJSON{
				SurvivingLines: m.Raw.SurvivingLines, AuthoredLines: m.Raw.AuthoredLines, SurvivalPct: round2(m.Raw.SurvivalPct()),
			}},
		},
		Authorship: authorshipJSON{
			HumanCommits: m.Raw.HumanCommits,
			AgentCommits: m.Raw.AgentCommits,
			AgentPct:     m.Raw.AgentPct(),
		},
	}
}

func toWeightsJSON(w contribution.Weights) weightsJSON {
	return weightsJSON{Shipped: w.Shipped, Review: w.Review, Effort: w.Effort, Quality: w.Quality, Ownership: w.Ownership, Durability: w.Durability}
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// ── Handlers ────────────────────────────────────────────────────────────────

// GET /api/contribution
func (h *contributionHandlers) report(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	p, ok := h.parsePeriod(w, r)
	if !ok {
		return
	}
	// Backfill involvement for the window so shipped/review/ownership populate on
	// synced data before scoring (idempotent; failures are non-fatal).
	h.ensureInvolvement(r.Context(), orgID, p)
	rep, err := h.svc.Compute(r.Context(), orgID, p)
	if err != nil {
		slog.Error("contribution report", "err", err)
		writeError(w, http.StatusInternalServerError, "could not compute contribution")
		return
	}
	members := make([]memberJSON, 0, len(rep.Members))
	for _, m := range rep.Members {
		members = append(members, toMemberJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period":  rep.Period,
		"weights": toWeightsJSON(rep.Weights),
		"members": members,
	})
}

// GET /api/contribution/{userId}
func (h *contributionHandlers) member(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	userID := r.PathValue("userId")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId required")
		return
	}
	p, ok := h.parsePeriod(w, r)
	if !ok {
		return
	}
	// Same window-backfill as the roster so the member's dimensions/evidence are
	// scored against fresh involvement (idempotent; non-fatal on failure).
	h.ensureInvolvement(r.Context(), orgID, p)
	detail, found, err := h.svc.ComputeMember(r.Context(), orgID, userID, p)
	if err != nil {
		slog.Error("contribution member", "err", err)
		writeError(w, http.StatusInternalServerError, "could not compute member contribution")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no contribution data for this member in the period")
		return
	}

	mj := toMemberJSON(detail.Member)
	writeJSON(w, http.StatusOK, map[string]any{
		"userId":     mj.UserID,
		"name":       mj.Name,
		"email":      mj.Email,
		"isAgentBot": mj.IsAgentBot,
		"composite":  mj.Composite,
		"dimensions": mj.Dimensions,
		"authorship": mj.Authorship,
		"evidence": map[string]any{
			"shipped":          evItems(detail.Evidence.Shipped),
			"review":           evItems(detail.Evidence.Review),
			"quality":          evItems(detail.Evidence.Quality),
			"effort":           evItems(detail.Evidence.Effort),
			"durability":       durabilityItems(detail.Evidence.Durability),
			"bugIntroductions": bugIntroItems(detail.Evidence.BugIntros),
		},
	})
}

// evItems guarantees [] (never null) for each evidence list.
func evItems(in []store.ContribEvidenceItem) []store.ContribEvidenceItem {
	if in == nil {
		return []store.ContribEvidenceItem{}
	}
	return in
}

// durabilityItems guarantees [] (never null) for the durability evidence list.
func durabilityItems(in []store.DurabilityEvidenceItem) []store.DurabilityEvidenceItem {
	if in == nil {
		return []store.DurabilityEvidenceItem{}
	}
	return in
}

// bugIntroItems guarantees [] (never null) for the SZZ bug-introduction list.
func bugIntroItems(in []store.BugIntroEvidenceItem) []store.BugIntroEvidenceItem {
	if in == nil {
		return []store.BugIntroEvidenceItem{}
	}
	return in
}

// GET /api/contribution/weights
func (h *contributionHandlers) getWeights(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	wts, err := h.svc.GetWeights(r.Context(), orgID)
	if err != nil {
		slog.Error("contribution get weights", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load weights")
		return
	}
	writeJSON(w, http.StatusOK, toWeightsJSON(wts))
}

// PUT /api/contribution/weights — owner/admin only.
func (h *contributionHandlers) putWeights(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can set contribution weights")
		return
	}

	var body weightsJSON
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Shipped < 0 || body.Review < 0 || body.Effort < 0 || body.Quality < 0 || body.Ownership < 0 || body.Durability < 0 {
		writeError(w, http.StatusBadRequest, "weights must be non-negative")
		return
	}
	out, err := h.svc.SetWeights(r.Context(), orgID, contribution.Weights{
		Shipped: body.Shipped, Review: body.Review, Effort: body.Effort,
		Quality: body.Quality, Ownership: body.Ownership, Durability: body.Durability,
	})
	if err != nil {
		slog.Error("contribution set weights", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save weights")
		return
	}
	writeJSON(w, http.StatusOK, toWeightsJSON(out))
}
