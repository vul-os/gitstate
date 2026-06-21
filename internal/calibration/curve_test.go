package calibration

import (
	"math"
	"testing"
	"time"
)

func TestDefaultSecsForDifficultyMonotonic(t *testing.T) {
	prev := 0.0
	for d := 1.0; d <= 10.0; d += 0.5 {
		got := DefaultSecsForDifficulty(d)
		if got <= prev {
			t.Errorf("DefaultSecsForDifficulty not increasing at d=%.1f: %.0f <= %.0f", d, got, prev)
		}
		prev = got
	}
	// Clamping outside 1–10.
	if DefaultSecsForDifficulty(0) != DefaultSecsForDifficulty(1) {
		t.Error("difficulty<1 should clamp to 1")
	}
	if DefaultSecsForDifficulty(99) != DefaultSecsForDifficulty(10) {
		t.Error("difficulty>10 should clamp to 10")
	}
	// Sanity scale: d=1 ≈ 30min, d=10 in the multi-day range.
	if d1 := DefaultSecsForDifficulty(1); math.Abs(d1-1800) > 1 {
		t.Errorf("d=1 want ~1800s, got %.0f", d1)
	}
	if d10 := DefaultSecsForDifficulty(10); d10 < 5*24*3600 {
		t.Errorf("d=10 want multi-day, got %.0f s", d10)
	}
}

func TestDifficultyBucket(t *testing.T) {
	cases := map[float64]int{1.0: 1, 1.4: 1, 1.5: 2, 4.6: 5, 9.9: 10, 0.2: 1, 11: 10}
	for in, want := range cases {
		if got := DifficultyBucket(in); got != want {
			t.Errorf("DifficultyBucket(%.1f) = %d, want %d", in, got, want)
		}
	}
}

func TestShrinkToPrior(t *testing.T) {
	const cohort, prior, k = 100.0, 1000.0, 8.0

	// n=0 ⇒ pure prior.
	if got := ShrinkToPrior(cohort, prior, 0, k); got != prior {
		t.Errorf("n=0 want prior %.0f, got %.0f", prior, got)
	}
	// n=k ⇒ exact midpoint.
	if got := ShrinkToPrior(cohort, prior, 8, k); math.Abs(got-550) > 1e-9 {
		t.Errorf("n=k want 550, got %.4f", got)
	}
	// Large n ⇒ approaches cohort.
	if got := ShrinkToPrior(cohort, prior, 1000, k); math.Abs(got-cohort) > 10 {
		t.Errorf("large n want ≈cohort, got %.2f", got)
	}
	// Monotonic in n: more evidence pulls away from prior toward cohort.
	prev := prior
	for n := 0; n <= 50; n++ {
		got := ShrinkToPrior(cohort, prior, n, k)
		if got > prev+1e-9 {
			t.Errorf("not monotonically decreasing toward cohort at n=%d: %.4f > %.4f", n, got, prev)
		}
		prev = got
	}
}

func TestRecencyWeight(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hl := 90 * 24 * time.Hour

	// Age 0 ⇒ weight 1.
	if w := RecencyWeight(now, now, hl); w != 1 {
		t.Errorf("age 0 want 1, got %f", w)
	}
	// One half-life ago ⇒ ~0.5.
	if w := RecencyWeight(now, now.Add(-hl), hl); math.Abs(w-0.5) > 1e-9 {
		t.Errorf("one half-life want 0.5, got %f", w)
	}
	// Two half-lives ⇒ ~0.25.
	if w := RecencyWeight(now, now.Add(-2*hl), hl); math.Abs(w-0.25) > 1e-9 {
		t.Errorf("two half-lives want 0.25, got %f", w)
	}
	// Future merge clamps to 1.
	if w := RecencyWeight(now, now.Add(time.Hour), hl); w != 1 {
		t.Errorf("future want 1, got %f", w)
	}
}

func TestWeightedQuantiles(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hl := 90 * 24 * time.Hour

	// Empty ⇒ ok=false.
	if _, _, _, _, _, ok := WeightedQuantiles(nil, now, hl); ok {
		t.Error("empty samples should be ok=false")
	}

	// Equal weights (all merged now): classic quantiles over 100..500.
	eq := []Sample{
		{ActualSecs: 100, MergedAt: now},
		{ActualSecs: 200, MergedAt: now},
		{ActualSecs: 300, MergedAt: now},
		{ActualSecs: 400, MergedAt: now},
		{ActualSecs: 500, MergedAt: now},
	}
	p25, med, p75, mean, n, ok := WeightedQuantiles(eq, now, hl)
	if !ok || n != 5 {
		t.Fatalf("equal: ok=%v n=%d", ok, n)
	}
	if math.Abs(med-300) > 1e-6 {
		t.Errorf("equal median want 300, got %.4f", med)
	}
	if math.Abs(mean-300) > 1e-6 {
		t.Errorf("equal mean want 300, got %.4f", mean)
	}
	if !(p25 >= 100 && p25 < med && p75 > med && p75 <= 500) {
		t.Errorf("quantile ordering off: p25=%.1f med=%.1f p75=%.1f", p25, med, p75)
	}

	// Recency pull: an old huge value should be down-weighted so the weighted
	// median stays near the recent small cluster.
	skew := []Sample{
		{ActualSecs: 100, MergedAt: now},
		{ActualSecs: 110, MergedAt: now},
		{ActualSecs: 120, MergedAt: now},
		{ActualSecs: 100000, MergedAt: now.Add(-10 * hl)}, // ancient outlier, weight ~2^-10
	}
	_, medSkew, _, meanSkew, _, _ := WeightedQuantiles(skew, now, hl)
	if medSkew > 1000 {
		t.Errorf("recency-weighted median should ignore ancient outlier, got %.1f", medSkew)
	}
	// The weighted mean is likewise dominated by recent samples.
	if meanSkew > 5000 {
		t.Errorf("recency-weighted mean should be dominated by recent samples, got %.1f", meanSkew)
	}
}
