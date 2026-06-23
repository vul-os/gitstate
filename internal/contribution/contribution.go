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
	DimShipped    = "shipped"
	DimReview     = "review"
	DimEffort     = "effort"
	DimQuality    = "quality"
	DimOwnership  = "ownership"
	DimDurability = "durability"
)

// dimOrder fixes a stable iteration order for deterministic output.
var dimOrder = []string{DimShipped, DimReview, DimEffort, DimQuality, DimOwnership, DimDurability}

// ── Weights ───────────────────────────────────────────────────────────────────

// Weights are the org's relative emphasis per dimension. Any non-negative scale
// is accepted; Normalized() rescales them to sum 1 for the composite. Defaults
// mirror the migration (shipped 30, review 20, effort 20, quality 15, ownership 15,
// durability 15 — added by 20260619_010).
type Weights struct {
	Shipped    float64 `json:"shipped"`
	Review     float64 `json:"review"`
	Effort     float64 `json:"effort"`
	Quality    float64 `json:"quality"`
	Ownership  float64 `json:"ownership"`
	Durability float64 `json:"durability"`
}

// DefaultWeights matches contribution_weights' column defaults.
func DefaultWeights() Weights {
	return Weights{Shipped: 30, Review: 20, Effort: 20, Quality: 15, Ownership: 15, Durability: 15}
}

// Normalized returns weights rescaled so the six components sum to 1. Negative
// inputs are clamped to 0. If every weight is 0 (or negative), the dimensions
// are weighted equally (1/6 each) so the composite is still meaningful.
func (w Weights) Normalized() Weights {
	s := nz(w.Shipped) + nz(w.Review) + nz(w.Effort) + nz(w.Quality) + nz(w.Ownership) + nz(w.Durability)
	if s <= 0 {
		eq := 1.0 / 6.0
		return Weights{Shipped: eq, Review: eq, Effort: eq, Quality: eq, Ownership: eq, Durability: eq}
	}
	return Weights{
		Shipped:    nz(w.Shipped) / s,
		Review:     nz(w.Review) / s,
		Effort:     nz(w.Effort) / s,
		Quality:    nz(w.Quality) / s,
		Ownership:  nz(w.Ownership) / s,
		Durability: nz(w.Durability) / s,
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
	UserID string
	// ContributorID is the canonical contributor (the PERSON) — the STABLE key the
	// API/frontend group by. Empty for a raw git identity not yet mapped.
	ContributorID string
	Name          string
	Email         string
	Login         string // primary git login for this identity (for evidence lookup)
	IsAgentBot    bool   // a bot/agent identity — shown separately, never inflates a human

	// shipped (ACCEPTED work only)
	MergedPRs       int
	IssuesClosed    int
	FeaturesShipped int

	// review
	ReviewsDone int

	// effort — sum of LLM difficulty over the member's MERGED PRs (NOT LOC)
	EffortPoints float64

	// quality (raw inputs; score is INVERTED in scoring)
	Reverts       int     // revert/hotfix/rollback commits authored
	AvgCycleHours float64 // mean lead time of merged PRs, hours (0 = unknown)

	// quality — deep signals (0 when the git-analysis pipeline hasn't run):
	BugsIntroduced   int // SZZ: changes later implicated as bug-introducing (more ⇒ worse)
	BugLines         int // SZZ: total lines of those introductions
	TestFileTouches  int // commit_files touches flagged is_test
	TotalFileTouches int // all commit_files touches

	// ownership
	AreasOwned int

	// durability — git-blame line survival (0 when not yet computed):
	SurvivingLines int
	AuthoredLines  int

	// authorship transparency
	HumanCommits int
	AgentCommits int
}

// SurvivalPct is the surviving fraction of authored lines in [0,1] (0 when no
// blame data). It is the texture shown in the durability raw.
func (m RawMember) SurvivalPct() float64 {
	if m.AuthoredLines <= 0 {
		return 0
	}
	p := float64(m.SurvivingLines) / float64(m.AuthoredLines)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// TestCoupling is tested-file-touches / total-file-touches in [0,1] (0 when no
// per-commit file data). Higher ⇒ the member touches tests more often.
func (m RawMember) TestCoupling() float64 {
	if m.TotalFileTouches <= 0 {
		return 0
	}
	return float64(m.TestFileTouches) / float64(m.TotalFileTouches)
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

// QualityInputs bundles every quality signal for one member. The deep signals
// (bugsIntroduced/bugLines from SZZ, testTouches/totalTouches from commit_files)
// are 0 when the git-analysis pipeline hasn't run — in which case QualityRaw
// degrades cleanly to the revert/hotfix + cycle-time behaviour.
type QualityInputs struct {
	Reverts        int
	MergedPRs      int
	AvgCycleHours  float64
	BugsIntroduced int
	BugLines       int
	TestTouches    int
	TotalTouches   int
}

// QualityRaw turns a member's quality inputs into a single raw value where
// HIGHER IS BETTER, before normalization. Quality SCORE INVERTS the bad signals
// and rewards the good one:
//
//   - fewer reverts/hotfixes        ⇒ higher (existing heuristic)
//   - faster (lower) cycle time     ⇒ higher (existing heuristic)
//   - fewer SZZ bug-introductions   ⇒ higher (NEW: gaming-resistant — you can't
//     fake "my changes didn't cause later bug-fixes")
//   - more test-coupling            ⇒ higher (NEW: touching tests is rewarded)
//
// We combine the PENALTIES (reverts, cycle, bugs) into a decaying health in (0,1]:
//
//	health = 1 / (1 + revertRate + cycle + bugRate)
//
// then apply a BOUNDED test-coupling multiplier in [1, 1+maxTestBoost] so tests
// raise — never lower — the value, and an all-tests member can't run away with it:
//
//	raw = health * (1 + maxTestBoost*testCoupling)
//
// So 0 reverts + instant cycle + 0 bugs + lots of tests ⇒ best; many reverts/bugs
// + slow cycle + no tests ⇒ →0. The exact constants don't matter for the final
// score because Normalize re-ranks across the cohort; what matters is the
// MONOTONIC inversion (more bugs ⇒ lower) and the bounded test boost, which this
// guarantees and the tests assert.
func QualityRaw(in QualityInputs) float64 {
	revertRate := float64(in.Reverts)
	if in.MergedPRs > 0 {
		revertRate = float64(in.Reverts) / float64(in.MergedPRs)
	}
	// Compress cycle hours so an enormous outlier can't dominate (days→~1).
	cycle := 0.0
	if in.AvgCycleHours > 0 {
		cycle = in.AvgCycleHours / (in.AvgCycleHours + 24.0) // 0 at instant, →1 as it grows
	}
	// SZZ bug penalty: normalized per merged PR when known (so a prolific shipper
	// isn't punished for raw volume), absolute otherwise; compressed so a huge bug
	// count can't make health negative or NaN.
	bugRate := float64(in.BugsIntroduced)
	if in.MergedPRs > 0 {
		bugRate = float64(in.BugsIntroduced) / float64(in.MergedPRs)
	}
	if bugRate < 0 {
		bugRate = 0
	}

	health := 1.0 / (1.0 + revertRate + cycle + bugRate)
	if math.IsNaN(health) || health < 0 {
		return 0
	}

	// Bounded test-coupling boost: tests can lift quality by up to maxTestBoost
	// (50%) but never reduce it. testCoupling already ∈ [0,1].
	const maxTestBoost = 0.5
	tc := 0.0
	if in.TotalTouches > 0 {
		tc = float64(in.TestTouches) / float64(in.TotalTouches)
		if tc < 0 {
			tc = 0
		} else if tc > 1 {
			tc = 1
		}
	}
	raw := health * (1.0 + maxTestBoost*tc)
	if math.IsNaN(raw) || raw < 0 {
		return 0
	}
	return raw
}

// DurabilityRaw turns a member's blame line-survival into a raw value where
// HIGHER IS BETTER, before normalization: it rewards code that PERSISTS.
//
//	raw = survivalFraction * survivingLines
//
// i.e. the surviving FRACTION (quality of persistence) scaled by the VOLUME of
// surviving lines (so a tiny but fully-surviving change doesn't outrank a large
// durable contribution). Someone whose lines were all overwritten has 0 surviving
// lines ⇒ raw 0, even with massive churn — exactly the anti-gaming property we
// want. authoredLines==0 (no blame data) ⇒ raw 0.
func DurabilityRaw(survivingLines, authoredLines int) float64 {
	if authoredLines <= 0 || survivingLines <= 0 {
		return 0
	}
	frac := float64(survivingLines) / float64(authoredLines)
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	raw := frac * float64(survivingLines)
	if math.IsNaN(raw) || raw < 0 {
		return 0
	}
	return raw
}

// DimensionScores holds the six 0–100 normalized dimension scores for one member.
type DimensionScores struct {
	Shipped    float64
	Review     float64
	Effort     float64
	Quality    float64
	Ownership  float64
	Durability float64
}

// Composite combines the six normalized dimension scores into a single 0–100
// number using the (already-normalized) weights. Because every input is 0–100
// and the weights sum to 1, the result is bounded to 0–100.
func Composite(d DimensionScores, w Weights) float64 {
	wn := w.Normalized()
	c := d.Shipped*wn.Shipped +
		d.Review*wn.Review +
		d.Effort*wn.Effort +
		d.Quality*wn.Quality +
		d.Ownership*wn.Ownership +
		d.Durability*wn.Durability
	return round1(c)
}

// shippedRaw collapses the three accepted-work signals into one raw value for
// normalization. Merged PRs and closed issues are the hard git/tool evidence;
// features_shipped is the human-curated texture. Equal weighting keeps it simple
// and explainable (the composite weights handle relative emphasis).
func shippedRaw(m RawMember) float64 {
	return float64(m.MergedPRs) + float64(m.IssuesClosed) + float64(m.FeaturesShipped)
}

// ── Extension hooks (now backed by the git-analysis pipeline) ──────────────────
//
// The blame line-survival, SZZ bug-introduction, and per-commit test-coupling
// signals are now REAL: the git-analysis pipeline populates author_survival,
// bug_introductions, and commit_files, which the store folds into RawMember
// (SurvivingLines/AuthoredLines, BugsIntroduced/BugLines, TestFileTouches/
// TotalFileTouches). The pure scoring layer turns those into the `durability`
// dimension (DurabilityRaw) and the enhanced `quality` raw (QualityRaw). When the
// pipeline hasn't run the fields are all 0, so durability scores 0 and quality
// degrades cleanly to the revert/cycle-time behaviour.
//
// The interfaces below remain as optional per-window OVERRIDES for a future
// service that wants to compute these in-process rather than read the tables;
// they are unused by the default Service.

// BlameSurvival measures how much of a member's authored code still survives in
// HEAD (a strong "did this work last?" outcome signal). Superseded by the
// author_survival table for the default Service; kept as an optional override.
type BlameSurvival interface {
	// SurvivalRate returns, in [0,1], the fraction of the member's added lines in
	// the window that are still present at HEAD. Higher = more durable work.
	SurvivalRate(ctx context.Context, orgID, userID string, from, to time.Time) (float64, error)
}

// SZZQuality flags bug-introducing changes via the SZZ algorithm (which commits
// a later fix/revert blames). Superseded by the bug_introductions table for the
// default Service; kept as an optional override.
type SZZQuality interface {
	// BugIntroRate returns, in [0,1], the share of the member's merged changes
	// later implicated as bug-introducing. LOWER is better (inverted into quality).
	BugIntroRate(ctx context.Context, orgID, userID string, from, to time.Time) (float64, error)
}

// ── Profile assembly (pure) ────────────────────────────────────────────────────

// Member is a fully scored member profile (the API shape mirrors this).
type Member struct {
	UserID        string
	ContributorID string
	Name          string
	Email         string
	IsAgentBot    bool
	Composite     float64
	Dimensions    DimensionScores
	Raw           RawMember
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

	// GAMING RESISTANCE: agent-bot identities are EXCLUDED from the cohort used for
	// within-project normalization. They are surfaced (flagged) in the output but
	// must never shift a human's percentile / min-max — otherwise a bot inflating a
	// dimension would deflate every human on it. humanIdx maps a position in the
	// human-only vectors back to the original raw index.
	humanIdx := make([]int, 0, n)
	for i, m := range raw {
		if !m.IsAgentBot {
			humanIdx = append(humanIdx, i)
		}
	}
	hn := len(humanIdx)

	// Build the raw value vectors per dimension over HUMANS ONLY.
	shipped := make([]float64, hn)
	review := make([]float64, hn)
	effort := make([]float64, hn)
	quality := make([]float64, hn)
	ownership := make([]float64, hn)
	durability := make([]float64, hn)
	for j, idx := range humanIdx {
		m := raw[idx]
		shipped[j] = shippedRaw(m)
		review[j] = float64(m.ReviewsDone)
		effort[j] = m.EffortPoints
		quality[j] = QualityRaw(QualityInputs{ // already inverted (higher=better)
			Reverts:        m.Reverts,
			MergedPRs:      m.MergedPRs,
			AvgCycleHours:  m.AvgCycleHours,
			BugsIntroduced: m.BugsIntroduced,
			BugLines:       m.BugLines,
			TestTouches:    m.TestFileTouches,
			TotalTouches:   m.TotalFileTouches,
		})
		ownership[j] = float64(m.AreasOwned)
		durability[j] = DurabilityRaw(m.SurvivingLines, m.AuthoredLines)
	}

	// Within-project normalization (0–100) per dimension, over humans only.
	sN := Normalize(shipped, method)
	rN := Normalize(review, method)
	eN := Normalize(effort, method)
	qN := Normalize(quality, method)
	oN := Normalize(ownership, method)
	dN := Normalize(durability, method)

	// Scatter the human scores back to their original indices.
	dimsByIdx := make(map[int]DimensionScores, hn)
	for j, idx := range humanIdx {
		dimsByIdx[idx] = DimensionScores{
			Shipped:    sN[j],
			Review:     rN[j],
			Effort:     eN[j],
			Quality:    qN[j],
			Ownership:  oN[j],
			Durability: dN[j],
		}
	}

	for i, m := range raw {
		// Bots are flagged in the output but carry zero normalized dimension scores
		// (they were excluded from the cohort, so they hold no rank among humans).
		dims := dimsByIdx[i]
		members[i] = Member{
			UserID:        m.UserID,
			ContributorID: m.ContributorID,
			Name:          m.Name,
			Email:         m.Email,
			IsAgentBot:    m.IsAgentBot,
			Composite:     Composite(dims, w),
			Dimensions:    dims,
			Raw:           m,
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
		w = Weights{Shipped: row.Shipped, Review: row.Review, Effort: row.Effort, Quality: row.Quality, Ownership: row.Ownership, Durability: row.Durability}
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
			OrgID:      orgID,
			Shipped:    nz(w.Shipped),
			Review:     nz(w.Review),
			Effort:     nz(w.Effort),
			Quality:    nz(w.Quality),
			Ownership:  nz(w.Ownership),
			Durability: nz(w.Durability),
		})
		if err != nil {
			return err
		}
		out = Weights{Shipped: row.Shipped, Review: row.Review, Effort: row.Effort, Quality: row.Quality, Ownership: row.Ownership, Durability: row.Durability}
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

	weights := Weights{Shipped: w.Shipped, Review: w.Review, Effort: w.Effort, Quality: w.Quality, Ownership: w.Ownership, Durability: w.Durability}
	members := Profiles(toRawMembers(raw), s.method, weights)
	return Report{Period: p, Weights: weights, Members: members}, nil
}

// toRawMembers maps store rows to the pure-layer RawMember type.
func toRawMembers(rows []store.ContribAggregate) []RawMember {
	out := make([]RawMember, len(rows))
	for i, r := range rows {
		out[i] = RawMember{
			UserID:           r.UserID,
			ContributorID:    r.ContributorID,
			Name:             r.Name,
			Email:            r.Email,
			Login:            r.Login,
			IsAgentBot:       r.IsAgentBot,
			MergedPRs:        r.MergedPRs,
			IssuesClosed:     r.IssuesClosed,
			FeaturesShipped:  r.FeaturesShipped,
			ReviewsDone:      r.ReviewsDone,
			EffortPoints:     r.EffortPoints,
			Reverts:          r.Reverts,
			AvgCycleHours:    r.AvgCycleHours,
			BugsIntroduced:   r.BugsIntroduced,
			BugLines:         r.BugLines,
			TestFileTouches:  r.TestFileTouches,
			TotalFileTouches: r.TotalFileTouches,
			AreasOwned:       r.AreasOwned,
			SurvivingLines:   r.SurvivingLines,
			AuthoredLines:    r.AuthoredLines,
			HumanCommits:     r.HumanCommits,
			AgentCommits:     r.AgentCommits,
		}
	}
	return out
}

// ── Member drill-down (with evidence) ──────────────────────────────────────────

// Evidence holds the real rows backing each dimension for one member — the
// drill-down that keeps texture honest (decisions P2: never a hidden rank).
type Evidence struct {
	Shipped    []store.ContribEvidenceItem    `json:"shipped"`
	Review     []store.ContribEvidenceItem    `json:"review"`
	Quality    []store.ContribEvidenceItem    `json:"quality"`
	Effort     []store.ContribEvidenceItem    `json:"effort"`
	Durability []store.DurabilityEvidenceItem `json:"durability"`
	// BugIntros are the SZZ bug-introductions surfaced under the quality dimension.
	BugIntros []store.BugIntroEvidenceItem `json:"bugIntroductions"`
}

// MemberDetail is one scored member PLUS the evidence backing each dimension.
type MemberDetail struct {
	Member
	Evidence Evidence `json:"evidence"`
}

// ComputeMember returns the scored profile for one PERSON (scored within the SAME
// cohort as the full report, so the numbers match) plus the evidence rows. `id`
// may be either a contributor id (the stable per-person key — used when a grouped
// person is clicked) or a linked user id (back-compat for member drill-down).
// Returns ok=false when the person has no contribution rows in the period.
func (s *Service) ComputeMember(ctx context.Context, orgID, id string, p Period) (MemberDetail, bool, error) {
	rep, err := s.Compute(ctx, orgID, p)
	if err != nil {
		return MemberDetail{}, false, err
	}
	// Prefer a contributor-id match (the stable key the roster sends), then fall
	// back to a user-id match so linked-member drill-downs keep working.
	var found *Member
	for i := range rep.Members {
		if rep.Members[i].ContributorID != "" && rep.Members[i].ContributorID == id {
			found = &rep.Members[i]
			break
		}
	}
	if found == nil {
		for i := range rep.Members {
			if rep.Members[i].UserID != "" && rep.Members[i].UserID == id {
				found = &rep.Members[i]
				break
			}
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
		ev = Evidence{Shipped: e.Shipped, Review: e.Review, Quality: e.Quality, Effort: e.Effort, Durability: e.Durability, BugIntros: e.BugIntros}
		return nil
	})
	if err != nil {
		return MemberDetail{}, false, err
	}
	return MemberDetail{Member: *found, Evidence: ev}, true, nil
}
