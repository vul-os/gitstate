// Package contribution — pure unit tests.
// These exercise the DB-free scoring core (normalization, composite weighting,
// the merge-gate, quality inversion, agent flagging) and always run — no
// DATABASE_URL or Postgres required.
package contribution

import (
	"math"
	"testing"
	"time"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ── Normalization ──────────────────────────────────────────────────────────────

func TestNormalize_Percentile(t *testing.T) {
	// Distinct ascending values: average-rank percentile.
	// n=4, sorted [1,2,3,4]. For value v: 100*(less + 0.5*equal)/n.
	//  1 → 100*(0+0.5)/4 = 12.5
	//  2 → 100*(1+0.5)/4 = 37.5
	//  3 → 100*(2+0.5)/4 = 62.5
	//  4 → 100*(3+0.5)/4 = 87.5
	got := Normalize([]float64{1, 2, 3, 4}, NormPercentile)
	want := []float64{12.5, 37.5, 62.5, 87.5}
	for i := range want {
		if !almostEqual(got[i], want[i]) {
			t.Errorf("percentile[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNormalize_PercentileTiesShareRank(t *testing.T) {
	// Ties must share a percentile — duplicating a contribution can't leapfrog.
	// values [5,5,5,10], n=4. For 5: less=0, equal=3 → 100*(0+1.5)/4 = 37.5.
	// For 10: less=3, equal=1 → 100*(3+0.5)/4 = 87.5.
	got := Normalize([]float64{5, 5, 5, 10}, NormPercentile)
	for i := 0; i < 3; i++ {
		if !almostEqual(got[i], 37.5) {
			t.Errorf("tied value[%d] = %v, want 37.5", i, got[i])
		}
	}
	if !almostEqual(got[3], 87.5) {
		t.Errorf("top value = %v, want 87.5", got[3])
	}
}

func TestNormalize_MinMax(t *testing.T) {
	got := Normalize([]float64{0, 5, 10}, NormMinMax)
	want := []float64{0, 50, 100}
	for i := range want {
		if !almostEqual(got[i], want[i]) {
			t.Errorf("minmax[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNormalize_EmptyCohort(t *testing.T) {
	if got := Normalize(nil, NormPercentile); len(got) != 0 {
		t.Errorf("empty cohort → len %d, want 0", len(got))
	}
}

func TestNormalize_AllZero(t *testing.T) {
	// Nobody contributed → everyone 0 (NOT 50, NOT div-by-zero).
	got := Normalize([]float64{0, 0, 0}, NormPercentile)
	for i, v := range got {
		if v != 0 {
			t.Errorf("all-zero[%d] = %v, want 0", i, v)
		}
	}
}

func TestNormalize_SingleMember(t *testing.T) {
	// A lone member can't be ranked; positive ⇒ 100, zero ⇒ 0.
	if got := Normalize([]float64{7}, NormPercentile); !almostEqual(got[0], 100) {
		t.Errorf("single positive = %v, want 100", got[0])
	}
	if got := Normalize([]float64{0}, NormPercentile); !almostEqual(got[0], 0) {
		t.Errorf("single zero = %v, want 0", got[0])
	}
}

func TestNormalize_NoSpread(t *testing.T) {
	// All equal & non-zero → nobody is above anybody → everyone full (and no
	// div-by-zero from max==min).
	got := Normalize([]float64{4, 4, 4}, NormMinMax)
	for i, v := range got {
		if !almostEqual(v, 100) {
			t.Errorf("no-spread[%d] = %v, want 100", i, v)
		}
	}
}

// ── Composite weighting ────────────────────────────────────────────────────────

func TestComposite_WeightedSum(t *testing.T) {
	d := DimensionScores{Shipped: 100, Review: 0, Effort: 50, Quality: 0, Ownership: 0}
	// Weights heavily on shipped: shipped 80, effort 20, others 0 → normalized
	// shipped=0.8, effort=0.2. composite = 100*0.8 + 50*0.2 = 90.
	w := Weights{Shipped: 80, Effort: 20}
	if got := Composite(d, w); !almostEqual(got, 90) {
		t.Errorf("composite = %v, want 90", got)
	}
}

func TestComposite_BoundedZeroTo100(t *testing.T) {
	d := DimensionScores{Shipped: 100, Review: 100, Effort: 100, Quality: 100, Ownership: 100}
	if got := Composite(d, DefaultWeights()); !almostEqual(got, 100) {
		t.Errorf("all-100 composite = %v, want 100", got)
	}
	z := DimensionScores{}
	if got := Composite(z, DefaultWeights()); !almostEqual(got, 0) {
		t.Errorf("all-0 composite = %v, want 0", got)
	}
}

func TestWeights_NormalizedSumsToOne(t *testing.T) {
	w := DefaultWeights().Normalized()
	sum := w.Shipped + w.Review + w.Effort + w.Quality + w.Ownership
	if !almostEqual(sum, 1) {
		t.Errorf("normalized weights sum = %v, want 1", sum)
	}
}

func TestWeights_AllZeroFallsBackToEqual(t *testing.T) {
	w := Weights{}.Normalized()
	for _, v := range []float64{w.Shipped, w.Review, w.Effort, w.Quality, w.Ownership} {
		if !almostEqual(v, 0.2) {
			t.Errorf("all-zero weights → %v, want 0.2 each", v)
		}
	}
}

func TestWeights_NegativeClampedToZero(t *testing.T) {
	w := Weights{Shipped: -5, Review: 10}.Normalized()
	if w.Shipped != 0 {
		t.Errorf("negative weight not clamped: %v", w.Shipped)
	}
	if !almostEqual(w.Review, 1) {
		t.Errorf("review weight = %v, want 1", w.Review)
	}
}

// ── Merge-gate ─────────────────────────────────────────────────────────────────
//
// The gate is applied at extraction (the store counts only merged PRs / closed
// issues into RawMember). At the pure layer we assert the consequence: a member
// whose shipped raw is 0 scores 0 on shipped relative to a peer who shipped.

func TestMergeGate_UnmergedScoresZero(t *testing.T) {
	raw := []RawMember{
		{UserID: "shipper", Name: "Shipper", MergedPRs: 5, IssuesClosed: 3},
		{UserID: "ghost", Name: "Ghost"}, // opened throwaway PRs that never merged → 0 shipped
	}
	got := Profiles(raw, NormPercentile, Weights{Shipped: 1})
	byID := map[string]Member{}
	for _, m := range got {
		byID[m.UserID] = m
	}
	if byID["ghost"].Dimensions.Shipped != 0 {
		t.Errorf("ghost shipped score = %v, want 0 (merge gate)", byID["ghost"].Dimensions.Shipped)
	}
	if byID["shipper"].Dimensions.Shipped <= byID["ghost"].Dimensions.Shipped {
		t.Errorf("shipper (%v) should outscore ghost (%v) on shipped",
			byID["shipper"].Dimensions.Shipped, byID["ghost"].Dimensions.Shipped)
	}
	// Composite respects it too.
	if byID["ghost"].Composite != 0 {
		t.Errorf("ghost composite = %v, want 0", byID["ghost"].Composite)
	}
}

// ── Quality inversion ──────────────────────────────────────────────────────────

func TestQualityRaw_InvertsReverts(t *testing.T) {
	clean := QualityRaw(0, 10, 5)  // no reverts
	dirty := QualityRaw(5, 10, 5)  // many reverts, same cycle
	if !(clean > dirty) {
		t.Errorf("fewer reverts should score HIGHER: clean=%v dirty=%v", clean, dirty)
	}
}

func TestQualityRaw_InvertsCycleTime(t *testing.T) {
	fast := QualityRaw(0, 10, 2)   // fast cycle
	slow := QualityRaw(0, 10, 200) // slow cycle, same reverts
	if !(fast > slow) {
		t.Errorf("faster cycle should score HIGHER: fast=%v slow=%v", fast, slow)
	}
}

func TestQuality_InversionFlowsToScore(t *testing.T) {
	// Member A: zero reverts, fast cycle → best quality.
	// Member B: many reverts, slow cycle → worst quality.
	raw := []RawMember{
		{UserID: "a", Name: "A", MergedPRs: 10, Reverts: 0, AvgCycleHours: 4},
		{UserID: "b", Name: "B", MergedPRs: 10, Reverts: 8, AvgCycleHours: 240},
	}
	got := Profiles(raw, NormPercentile, Weights{Quality: 1})
	byID := map[string]Member{}
	for _, m := range got {
		byID[m.UserID] = m
	}
	if !(byID["a"].Dimensions.Quality > byID["b"].Dimensions.Quality) {
		t.Errorf("clean member quality (%v) should beat dirty member (%v)",
			byID["a"].Dimensions.Quality, byID["b"].Dimensions.Quality)
	}
}

// ── Agent flagging & authorship transparency ────────────────────────────────────

func TestAgentFlag_Surfaced(t *testing.T) {
	raw := []RawMember{
		{UserID: "human", Name: "Human", MergedPRs: 3, HumanCommits: 20, AgentCommits: 0},
		{UserID: "bot", Name: "Bot", IsAgentBot: true, MergedPRs: 9, HumanCommits: 0, AgentCommits: 50},
	}
	got := Profiles(raw, NormPercentile, DefaultWeights())
	var bot, human *Member
	for i := range got {
		switch got[i].UserID {
		case "bot":
			bot = &got[i]
		case "human":
			human = &got[i]
		}
	}
	if bot == nil || human == nil {
		t.Fatal("expected both members present")
	}
	if !bot.IsAgentBot {
		t.Error("bot.IsAgentBot should be true (shown separately, not inflating a human)")
	}
	if human.IsAgentBot {
		t.Error("human.IsAgentBot should be false")
	}
}

func TestAgentPct(t *testing.T) {
	cases := []struct {
		human, agent int
		want         float64
	}{
		{20, 0, 0},
		{0, 0, 0},   // no commits → 0, not div-by-zero
		{50, 50, 50},
		{0, 10, 100},
		{1, 3, 75},
	}
	for _, c := range cases {
		m := RawMember{HumanCommits: c.human, AgentCommits: c.agent}
		if got := m.AgentPct(); !almostEqual(got, c.want) {
			t.Errorf("AgentPct(h=%d,a=%d) = %v, want %v", c.human, c.agent, got, c.want)
		}
	}
}

// ── Sorting & profile assembly ─────────────────────────────────────────────────

func TestProfiles_SortedByCompositeDesc(t *testing.T) {
	raw := []RawMember{
		{UserID: "low", Name: "Low", MergedPRs: 1},
		{UserID: "high", Name: "High", MergedPRs: 10, ReviewsDone: 10, AreasOwned: 5, EffortPoints: 50},
		{UserID: "mid", Name: "Mid", MergedPRs: 5, ReviewsDone: 2},
	}
	got := Profiles(raw, NormPercentile, DefaultWeights())
	for i := 1; i < len(got); i++ {
		if got[i-1].Composite < got[i].Composite {
			t.Errorf("not sorted desc: %v before %v", got[i-1].Composite, got[i].Composite)
		}
	}
	if got[0].UserID != "high" {
		t.Errorf("top member = %q, want high", got[0].UserID)
	}
}

func TestProfiles_Empty(t *testing.T) {
	if got := Profiles(nil, NormPercentile, DefaultWeights()); len(got) != 0 {
		t.Errorf("empty cohort → %d members, want 0", len(got))
	}
}

// ── Period resolution ──────────────────────────────────────────────────────────

func TestResolvePeriod_DefaultsTo90Days(t *testing.T) {
	now := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	p := ResolvePeriod(time.Time{}, time.Time{}, now)
	if !p.To.Equal(now) {
		t.Errorf("to = %v, want %v", p.To, now)
	}
	wantFrom := now.AddDate(0, 0, -DefaultRangeDays)
	if !p.From.Equal(wantFrom) {
		t.Errorf("from = %v, want %v", p.From, wantFrom)
	}
}

func TestResolvePeriod_SwapsInverted(t *testing.T) {
	now := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	hi := now
	lo := now.AddDate(0, 0, -10)
	p := ResolvePeriod(hi, lo, now) // inverted on purpose
	if p.From.After(p.To) {
		t.Errorf("period not corrected: from %v after to %v", p.From, p.To)
	}
}
