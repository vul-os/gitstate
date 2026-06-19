// Package contribution computes a multi-dimensional, evidence-backed,
// gaming-resistant view of how each member contributed to project OUTCOMES —
// the input an employee-owned company uses to *inform* (never auto-decide)
// share allocation. It is deliberately NOT a vanity leaderboard.
//
// Research grounding (already settled, implemented here):
//   - Single metrics get gamed (Goodhart's law; the SPACE framework). So we
//     never score a raw commit/LOC count.
//   - AI agents make commit/LOC counting worthless. So agent work is surfaced
//     transparently (humanCommits vs agentCommits, agentPct) and agent-bot
//     identities are flagged so they never silently inflate a human.
//
// What we DO measure, per member, per period:
//   - shipped   — ACCEPTED work only: MERGED PRs, closed issues, features shipped.
//   - review    — the invisible senior work (reviews_done) so mentors aren't zeroed.
//   - effort    — LLM-judged diff DIFFICULTY of merged PRs (explicitly NOT lines).
//   - quality   — INVERTED: fewer reverts/hotfixes + faster cycle ⇒ higher.
//   - ownership — areas owned / knowledge spread (bus-factor).
//
// Scoring pipeline (see Profiles):
//  1. GATES first — unmerged / unaccepted work contributes ~0 (gaming-resistant).
//  2. WITHIN-PROJECT NORMALIZATION — each dimension's raw value is normalized
//     across the project's members to 0–100 (so the number means "relative to
//     this team this period", never an absolute that can be farmed). Div-by-zero
//     and single-member cohorts are guarded.
//  3. COMPOSITE — weighted sum of the five 0–100 dimension scores using the
//     org's configurable weights (normalized to sum 1).
//
// The pure helpers below (Normalize, Composite, gate helpers, quality inversion)
// are unit-testable WITHOUT a database. The Service wires them to the store.
package contribution

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// DefaultRangeDays is the look-back window used when from/to are omitted.
const DefaultRangeDays = 90

// Dimension names — the canonical keys used everywhere (weights, JSON, scoring).
const (
	DimShipped   = "shipped"
	DimReview    = "review"
	DimEffort    = "effort"
	DimQuality   = "quality"
	DimOwnership = "ownership"
)

// dimOrder fixes a stable iteration order for deterministic output.
var dimOrder = []string{DimShipped, DimReview, DimEffort, DimQuality, DimOwnership}

// ── Weights ───────────────────────────────────────────────────────────────────

// Weights are the org's relative emphasis per dimension. Any non-negative scale
// is accepted; Normalized() rescales them to sum 1 for the composite. Defaults
// mirror the migration (shipped 30, review 20, effort 20, quality 15, ownership 15).
type Weights struct {
	Shipped   float64 `json:"shipped"`
	Review    float64 `json:"review"`
	Effort    float64 `json:"effort"`
	Quality   float64 `json:"quality"`
	Ownership float64 `json:"ownership"`
}

// DefaultWeights matches contribution_weights' column defaults.
func DefaultWeights() Weights {
	return Weights{Shipped: 30, Review: 20, Effort: 20, Quality: 15, Ownership: 15}
}

// Normalized returns weights rescaled so the five components sum to 1. Negative
// inputs are clamped to 0. If every weight is 0 (or negative), the dimensions
// are weighted equally (1/5 each) so the composite is still meaningful.
func (w Weights) Normalized() Weights {
	s := nz(w.Shipped) + nz(w.Review) + nz(w.Effort) + nz(w.Quality) + nz(w.Ownership)
	if s <= 0 {
		return Weights{Shipped: 0.2, Review: 0.2, Effort: 0.2, Quality: 0.2, Ownership: 0.2}
	}
	return Weights{
		Shipped:   nz(w.Shipped) / s,
		Review:    nz(w.Review) / s,
		Effort:    nz(w.Effort) / s,
		Quality:   nz(w.Quality) / s,
		Ownership: nz(w.Ownership) / s,
	}
}

func nz(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	return v
}

// ── Raw per-member aggregates (gates applied at extraction in the store) ───────

// RawMember is the merge-gated, period-scoped set of facts for one member,
// already mapped to a user identity. The store guarantees that "shipped" and
// "effort" counts already exclude unmerged work (the gate). The pure scoring
// layer takes these as given and only normalizes + weights.
type RawMember struct {
	UserID    string
	Name      string
	Email     string
	Login     string // primary git login for this identity (for evidence lookup)
	IsAgentBot bool  // a bot/agent identity — shown separately, never inflates a human

	// shipped (ACCEPTED work only)
	MergedPRs      int
	IssuesClosed   int
	FeaturesShipped int

	// review
	ReviewsDone int

	// effort — sum of LLM difficulty over the member's MERGED PRs (NOT LOC)
	EffortPoints float64

	// quality (raw inputs; score is INVERTED in scoring)
	Reverts       int     // revert/hotfix/rollback commits authored
	AvgCycleHours float64 // mean lead time of merged PRs, hours (0 = unknown)

	// ownership
	AreasOwned int

	// authorship transparency
	HumanCommits int
	AgentCommits int
}

// AgentPct is the share of this member's commits that were agent-authored.
func (m RawMember) AgentPct() float64 {
	total := m.HumanCommits + m.AgentCommits
	if total == 0 {
		return 0
	}
	return round1(100 * float64(m.AgentCommits) / float64(total))
}

// ── Pure scoring primitives ────────────────────────────────────────────────────

// NormMethod selects the within-project normalization strategy.
type NormMethod int

const (
	// NormPercentile maps each value to its rank percentile across the cohort
	// (0–100). Robust to outliers and to one member farming a single dimension —
	// the property we want for gaming resistance. Ties share the average rank.
	NormPercentile NormMethod = iota
	// NormMinMax does a linear (value-min)/(max-min) scaling to 0–100. Kept for
	// completeness / testing; percentile is the default.
	NormMinMax
)

// Normalize maps a slice of raw dimension values to 0–100 scores across the
// cohort (within-project normalization). It guards the degenerate cases that a
// naive min-max would mishandle:
//
//   - empty cohort        → empty result.
//   - single member       → 100 if value>0 else 0 (a lone member can't be ranked,
//     but a positive contribution still reads as "full" for that dimension; a
//     zero contribution reads as 0). This avoids a misleading 0 or 50.
//   - all values equal     → every member gets 100 if the shared value>0, else 0
//     (no spread ⇒ nobody is "above" anybody; div-by-zero avoided).
//   - all values zero      → all 0 (nobody contributed on this dimension).
//
// A raw value of exactly 0 ALWAYS maps to score 0, regardless of method or
// cohort: you cannot earn credit on a dimension you did not touch. This closes a
// gaming/percentile artefact where a do-nothing member would otherwise inherit a
// non-zero rank percentile just for existing in the cohort.
//
// Percentile is the default and recommended method (outlier/gaming resistant).
func Normalize(values []float64, method NormMethod) []float64 {
	n := len(values)
	out := make([]float64, n)
	if n == 0 {
		return out
	}

	min, max := values[0], values[0]
	allZero := true
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		if v != 0 {
			allZero = false
		}
	}
	if allZero {
		return out // all 0
	}

	// Single member: positive ⇒ 100, zero ⇒ 0.
	if n == 1 {
		if values[0] > 0 {
			out[0] = 100
		}
		return out
	}

	// No spread (all equal & non-zero): everyone full.
	if max == min {
		for i := range out {
			out[i] = 100
		}
		return out
	}

	switch method {
	case NormMinMax:
		span := max - min
		for i, v := range values {
			if v == 0 {
				continue // zero contribution → zero score
			}
			out[i] = round1(100 * (v - min) / span)
		}
	default: // NormPercentile
		// Average-rank percentile: for each value, percentile =
		// 100 * (count_less + 0.5*count_equal) / n. Ties share a percentile,
		// so duplicating a contribution can't leapfrog peers.
		for i, v := range values {
			if v == 0 {
				continue // zero contribution → zero score (gaming guard)
			}
			var less, equal float64
			for _, u := range values {
				switch {
				case u < v:
					less++
				case u == v:
					equal++
				}
			}
			out[i] = round1(100 * (less + 0.5*equal) / float64(n))
		}
	}
	return out
}

// QualityRaw turns a member's quality inputs into a single raw value where
// HIGHER IS BETTER, before normalization. Quality SCORE INVERTS the bad signals:
// fewer reverts and a faster (lower) cycle time both raise the raw value.
//
//	revertPenalty = reverts (per accepted PR if known; absolute otherwise)
//	cyclePenalty  = avgCycleHours
//
// We combine them as a decaying "health" value in [0,1]:
//
//	health = 1 / (1 + revertPenalty + cycleWeight*normalizedCycle)
//
// so 0 reverts + instant cycle ⇒ ~1 (best), and lots of reverts / very slow
// cycle ⇒ →0 (worst). The exact constant doesn't matter for the final score
// because Normalize re-ranks across the cohort; what matters is the MONOTONIC
// inversion, which this guarantees and the tests assert.
func QualityRaw(reverts int, mergedPRs int, avgCycleHours float64) float64 {
	revertRate := float64(reverts)
	if mergedPRs > 0 {
		revertRate = float64(reverts) / float64(mergedPRs)
	}
	// Compress cycle hours so an enormous outlier can't dominate (days→~1).
	cycle := 0.0
	if avgCycleHours > 0 {
		cycle = avgCycleHours / (avgCycleHours + 24.0) // 0 at instant, →1 as it grows
	}
	health := 1.0 / (1.0 + revertRate + cycle)
	if math.IsNaN(health) || health < 0 {
		return 0
	}
	return health
}

// DimensionScores holds the five 0–100 normalized dimension scores for one member.
type DimensionScores struct {
	Shipped   float64
	Review    float64
	Effort    float64
	Quality   float64
	Ownership float64
}

// Composite combines the five normalized dimension scores into a single 0–100
// number using the (already-normalized) weights. Because every input is 0–100
// and the weights sum to 1, the result is bounded to 0–100.
func Composite(d DimensionScores, w Weights) float64 {
	wn := w.Normalized()
	c := d.Shipped*wn.Shipped +
		d.Review*wn.Review +
		d.Effort*wn.Effort +
		d.Quality*wn.Quality +
		d.Ownership*wn.Ownership
	return round1(c)
}

// shippedRaw collapses the three accepted-work signals into one raw value for
// normalization. Merged PRs and closed issues are the hard git/tool evidence;
// features_shipped is the human-curated texture. Equal weighting keeps it simple
// and explainable (the composite weights handle relative emphasis).
func shippedRaw(m RawMember) float64 {
	return float64(m.MergedPRs) + float64(m.IssuesClosed) + float64(m.FeaturesShipped)
}

// ── Extension hooks (NOT YET WIRED — see comments) ─────────────────────────────
//
// There is currently no per-commit file-path or blame data in the schema, so we
// do NOT fabricate line-survival or test-coupling signals. These interfaces are
// the clean plug-in points for when real-repo sync lands; the engine already
// folds their output into the quality/effort raws via the hooks below.

// BlameSurvival measures how much of a member's authored code still survives in
// HEAD (a strong "did this work last?" outcome signal). TODO: implement once
// `git blame` history is synced per repo; until then the Service leaves it nil
// and blame survival contributes nothing.
type BlameSurvival interface {
	// SurvivalRate returns, in [0,1], the fraction of the member's added lines in
	// the window that are still present at HEAD. Higher = more durable work.
	SurvivalRate(ctx context.Context, orgID, userID string, from, to time.Time) (float64, error)
}

// SZZQuality flags bug-introducing changes via the SZZ algorithm (which commits
// a later fix/revert blames). TODO: implement once blame + fix-commit linkage is
// synced; until then quality relies on the revert/hotfix message heuristic and
// cycle-time health only.
type SZZQuality interface {
	// BugIntroRate returns, in [0,1], the share of the member's merged changes
	// later implicated as bug-introducing. LOWER is better (inverted into quality).
	BugIntroRate(ctx context.Context, orgID, userID string, from, to time.Time) (float64, error)
}

// ── Profile assembly (pure) ────────────────────────────────────────────────────

// Member is a fully scored member profile (the API shape mirrors this).
type Member struct {
	UserID     string
	Name       string
	Email      string
	IsAgentBot bool
	Composite  float64
	Dimensions DimensionScores
	Raw        RawMember
}

// Profiles is the pure core: given the merge-gated raw members, the normalization
// method, and the org weights, it produces fully scored, composite-sorted member
// profiles. No database, no time — trivially unit-testable.
//
// This is where the three-stage pipeline lives:
//  1. gates are already applied in RawMember (shipped/effort exclude unmerged work);
//  2. within-project normalization per dimension (incl. the quality inversion);
//  3. weighted composite.
func Profiles(raw []RawMember, method NormMethod, w Weights) []Member {
	n := len(raw)
	members := make([]Member, n)
	if n == 0 {
		return members
	}

	// Build the raw value vectors per dimension.
	shipped := make([]float64, n)
	review := make([]float64, n)
	effort := make([]float64, n)
	quality := make([]float64, n)
	ownership := make([]float64, n)
	for i, m := range raw {
		shipped[i] = shippedRaw(m)
		review[i] = float64(m.ReviewsDone)
		effort[i] = m.EffortPoints
		quality[i] = QualityRaw(m.Reverts, m.MergedPRs, m.AvgCycleHours) // already inverted (higher=better)
		ownership[i] = float64(m.AreasOwned)
	}

	// Within-project normalization (0–100) per dimension.
	sN := Normalize(shipped, method)
	rN := Normalize(review, method)
	eN := Normalize(effort, method)
	qN := Normalize(quality, method)
	oN := Normalize(ownership, method)

	for i, m := range raw {
		dims := DimensionScores{
			Shipped:   sN[i],
			Review:    rN[i],
			Effort:    eN[i],
			Quality:   qN[i],
			Ownership: oN[i],
		}
		members[i] = Member{
			UserID:     m.UserID,
			Name:       m.Name,
			Email:      m.Email,
			IsAgentBot: m.IsAgentBot,
			Composite:  Composite(dims, w),
			Dimensions: dims,
			Raw:        m,
		}
	}

	// Sort by composite desc; tie-break by name for stable output.
	sort.SliceStable(members, func(a, b int) bool {
		if members[a].Composite != members[b].Composite {
			return members[a].Composite > members[b].Composite
		}
		return strings.ToLower(members[a].Name) < strings.ToLower(members[b].Name)
	})
	return members
}

func round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

// ── Service (DB-backed) ────────────────────────────────────────────────────────

// Service loads the raw aggregates (org-scoped, via db.WithOrg) and runs the
// pure Profiles core over them. Normalization method is fixed to percentile (the
// gaming-resistant default); it is exposed as a field so a future setting can
// switch it without touching the API.
type Service struct {
	db     *db.DB
	method NormMethod

	// Optional, nil until real-repo sync lands (see BlameSurvival / SZZQuality).
	blame BlameSurvival //nolint:unused // extension hook, wired when blame sync exists
	szz   SZZQuality    //nolint:unused // extension hook, wired when SZZ linkage exists
}

// New constructs a Service bound to a DB pool, using percentile normalization.
func New(database *db.DB) *Service {
	return &Service{db: database, method: NormPercentile}
}

// Period is the resolved [From,To] window for a request.
type Period struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// ResolvePeriod defaults an omitted window to the last DefaultRangeDays days.
// `now` is injected so callers/tests are deterministic.
func ResolvePeriod(from, to time.Time, now time.Time) Period {
	p := Period{From: from, To: to}
	switch {
	case p.From.IsZero() && p.To.IsZero():
		p.To = now
		p.From = now.AddDate(0, 0, -DefaultRangeDays)
	case p.From.IsZero():
		p.From = p.To.AddDate(0, 0, -DefaultRangeDays)
	case p.To.IsZero():
		p.To = now
	}
	if p.From.After(p.To) {
		p.From, p.To = p.To, p.From
	}
	return p
}

// GetWeights returns the org's configured weights, defaulting to DefaultWeights
// when no row exists.
func (s *Service) GetWeights(ctx context.Context, orgID string) (Weights, error) {
	var w Weights
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		row, err := store.GetContributionWeights(ctx, tx, orgID)
		if err != nil {
			return err
		}
		w = Weights{Shipped: row.Shipped, Review: row.Review, Effort: row.Effort, Quality: row.Quality, Ownership: row.Ownership}
		return nil
	})
	if err != nil {
		return Weights{}, err
	}
	return w, nil
}

// SetWeights upserts the org's weights (caller enforces owner/admin).
func (s *Service) SetWeights(ctx context.Context, orgID string, w Weights) (Weights, error) {
	var out Weights
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		row, err := store.UpsertContributionWeights(ctx, tx, store.ContributionWeights{
			OrgID:     orgID,
			Shipped:   nz(w.Shipped),
			Review:    nz(w.Review),
			Effort:    nz(w.Effort),
			Quality:   nz(w.Quality),
			Ownership: nz(w.Ownership),
		})
		if err != nil {
			return err
		}
		out = Weights{Shipped: row.Shipped, Review: row.Review, Effort: row.Effort, Quality: row.Quality, Ownership: row.Ownership}
		return nil
	})
	if err != nil {
		return Weights{}, err
	}
	return out, nil
}

// Report is the full /api/contribution payload.
type Report struct {
	Period  Period   `json:"period"`
	Weights Weights  `json:"weights"`
	Members []Member `json:"members"`
}

// Compute loads the raw aggregates for the period and returns scored profiles.
func (s *Service) Compute(ctx context.Context, orgID string, p Period) (Report, error) {
	var (
		raw []store.ContribAggregate
		w   store.ContributionWeights
	)
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if w, err = store.GetContributionWeights(ctx, tx, orgID); err != nil {
			return err
		}
		raw, err = store.LoadContributionAggregates(ctx, tx, orgID, p.From, p.To)
		return err
	})
	if err != nil {
		return Report{}, err
	}

	weights := Weights{Shipped: w.Shipped, Review: w.Review, Effort: w.Effort, Quality: w.Quality, Ownership: w.Ownership}
	members := Profiles(toRawMembers(raw), s.method, weights)
	return Report{Period: p, Weights: weights, Members: members}, nil
}

// toRawMembers maps store rows to the pure-layer RawMember type.
func toRawMembers(rows []store.ContribAggregate) []RawMember {
	out := make([]RawMember, len(rows))
	for i, r := range rows {
		out[i] = RawMember{
			UserID:          r.UserID,
			Name:            r.Name,
			Email:           r.Email,
			Login:           r.Login,
			IsAgentBot:      r.IsAgentBot,
			MergedPRs:       r.MergedPRs,
			IssuesClosed:    r.IssuesClosed,
			FeaturesShipped: r.FeaturesShipped,
			ReviewsDone:     r.ReviewsDone,
			EffortPoints:    r.EffortPoints,
			Reverts:         r.Reverts,
			AvgCycleHours:   r.AvgCycleHours,
			AreasOwned:      r.AreasOwned,
			HumanCommits:    r.HumanCommits,
			AgentCommits:    r.AgentCommits,
		}
	}
	return out
}

// ── Member drill-down (with evidence) ──────────────────────────────────────────

// Evidence holds the real rows backing each dimension for one member — the
// drill-down that keeps texture honest (decisions P2: never a hidden rank).
type Evidence struct {
	Shipped []store.ContribEvidenceItem `json:"shipped"`
	Review  []store.ContribEvidenceItem `json:"review"`
	Quality []store.ContribEvidenceItem `json:"quality"`
	Effort  []store.ContribEvidenceItem `json:"effort"`
}

// MemberDetail is one scored member PLUS the evidence backing each dimension.
type MemberDetail struct {
	Member
	Evidence Evidence `json:"evidence"`
}

// ComputeMember returns the scored profile for one user (scored within the SAME
// cohort as the full report, so the numbers match) plus the evidence rows.
// Returns ok=false when the user has no contribution rows in the period.
func (s *Service) ComputeMember(ctx context.Context, orgID, userID string, p Period) (MemberDetail, bool, error) {
	rep, err := s.Compute(ctx, orgID, p)
	if err != nil {
		return MemberDetail{}, false, err
	}
	var found *Member
	for i := range rep.Members {
		if rep.Members[i].UserID == userID {
			found = &rep.Members[i]
			break
		}
	}
	if found == nil {
		return MemberDetail{}, false, nil
	}

	var ev Evidence
	err = s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e store.ContribEvidence
		var err error
		e, err = store.LoadContributionEvidence(ctx, tx, orgID, found.Email, found.Raw.Login, p.From, p.To)
		if err != nil {
			return err
		}
		ev = Evidence{Shipped: e.Shipped, Review: e.Review, Quality: e.Quality, Effort: e.Effort}
		return nil
	})
	if err != nil {
		return MemberDetail{}, false, err
	}
	return MemberDetail{Member: *found, Evidence: ev}, true, nil
}
