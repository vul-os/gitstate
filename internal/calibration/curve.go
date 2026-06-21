package calibration

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// Tunable constants for the calibration read/compute path. They are package
// vars (not consts) only so tests can document them; production never mutates.
var (
	// MinCohortN is the sample floor for a cohort to be USED directly. Below it
	// we either shrink toward the global prior or walk to a coarser cohort.
	MinCohortN = 5

	// ShrinkageK is the empirical-Bayes pseudo-count: the calibrated median is
	// (n*cohortMedian + k*globalMedian)/(n+k). Larger k ⇒ trust the prior more
	// until the cohort accumulates evidence.
	ShrinkageK = 8.0

	// RecencyHalfLife is the age at which a merged PR's weight halves in the
	// recency-weighted quantiles. Recent merges dominate the curve.
	RecencyHalfLife = 90 * 24 * time.Hour
)

// ── Cold-start prior ──────────────────────────────────────────────────────────

// DefaultSecsForDifficulty is the fixed difficulty→seconds curve used when the
// org has NO calibration data at all (true cold start) and as the global prior's
// floor. It is intentionally smooth and monotonic: difficulty 1 ≈ 30min,
// difficulty 5 ≈ 1 day of focused work, difficulty 10 ≈ ~2 weeks. The shape is
// exponential because semantic difficulty compounds (a 10 is not 10× a 1).
func DefaultSecsForDifficulty(difficulty float64) float64 {
	d := clampDifficulty(difficulty)
	// 1800s (30m) at d=1, growing ~3.5× per +2 difficulty → ~9.7e5s (~11d) at d=10.
	const base = 1800.0
	return base * math.Pow(3.5, (d-1)/2.0)
}

func clampDifficulty(d float64) float64 {
	if d < 1 {
		return 1
	}
	if d > 10 {
		return 10
	}
	return d
}

// DifficultyBucket rounds a 1–10 difficulty to the integer bucket used as the
// effort_calibration.difficulty_bucket key.
func DifficultyBucket(difficulty float64) int {
	return int(math.Round(clampDifficulty(difficulty)))
}

// ── Recency-weighted quantiles ────────────────────────────────────────────────

// Sample is one observed (merged) outcome: the actual lead time in seconds and
// the merge timestamp used to age-weight it.
type Sample struct {
	ActualSecs int64
	MergedAt   time.Time
}

// RecencyWeight returns the exponential-decay weight of a sample given its age
// relative to now, with the configured half-life. age<=0 ⇒ weight 1.
func RecencyWeight(now, mergedAt time.Time, halfLife time.Duration) float64 {
	age := now.Sub(mergedAt)
	if age <= 0 || halfLife <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(age)/float64(halfLife))
}

// WeightedQuantiles computes the recency-weighted p25/median/p75/mean over the
// samples. Returns ok=false when there are no samples. The weighted quantile is
// the classic cumulative-weight interpolation: the value at which the running
// weight fraction crosses q.
func WeightedQuantiles(samples []Sample, now time.Time, halfLife time.Duration) (p25, median, p75, mean float64, n int, ok bool) {
	if len(samples) == 0 {
		return 0, 0, 0, 0, 0, false
	}

	type wv struct {
		v float64
		w float64
	}
	pts := make([]wv, 0, len(samples))
	var totalW, weightedSum float64
	for _, s := range samples {
		w := RecencyWeight(now, s.MergedAt, halfLife)
		if w <= 0 {
			continue
		}
		v := float64(s.ActualSecs)
		pts = append(pts, wv{v: v, w: w})
		totalW += w
		weightedSum += v * w
	}
	if len(pts) == 0 || totalW <= 0 {
		return 0, 0, 0, 0, 0, false
	}

	sort.Slice(pts, func(i, j int) bool { return pts[i].v < pts[j].v })

	q := func(target float64) float64 {
		// Cumulative weight at the MIDPOINT of each point's weight band, then
		// linearly interpolate between bracketing points (Type-7-like).
		cum := 0.0
		prevV, prevC := pts[0].v, 0.0
		for i, p := range pts {
			midC := (cum + p.w/2) / totalW
			if midC >= target {
				if i == 0 {
					return p.v
				}
				span := midC - prevC
				if span <= 0 {
					return p.v
				}
				frac := (target - prevC) / span
				return prevV + frac*(p.v-prevV)
			}
			cum += p.w
			prevV, prevC = p.v, midC
		}
		return pts[len(pts)-1].v
	}

	mean = weightedSum / totalW
	return q(0.25), q(0.50), q(0.75), mean, len(pts), true
}

// ShrinkToPrior applies empirical-Bayes shrinkage of a cohort estimate toward a
// prior given the cohort's sample size n and pseudo-count k:
//
//	est = (n*cohort + k*prior) / (n + k)
//
// As n→∞ the cohort dominates; at n=0 it returns the prior. Pure + testable.
func ShrinkToPrior(cohort, prior float64, n int, k float64) float64 {
	fn := float64(n)
	if fn < 0 {
		fn = 0
	}
	if fn+k == 0 {
		return prior
	}
	return (fn*cohort + k*prior) / (fn + k)
}

// ── CalibratedSecs (read path) ────────────────────────────────────────────────

// CalibratedResult is the outcome of a calibration lookup.
type CalibratedResult struct {
	PredictedSecs float64
	// CohortKey is the cohort actually used (after walking richest-first). It is
	// GlobalCohort on the cold-start default path.
	CohortKey string
	// Shrunk is true when empirical-Bayes shrinkage toward the global prior was
	// applied (the chosen cohort had 0 < n < MinCohortN-equivalent confidence).
	Shrunk bool
	// ColdStart is true when no org data existed and the fixed prior was used.
	ColdStart bool
}

// CalibratedSecs converts a model difficulty into calibrated seconds for the
// given org. It walks cohortCandidates RICHEST-FIRST and selects the first
// cohort with n ≥ MinCohortN at the rounded difficulty bucket. When the best
// available cohort is sparse it applies empirical-Bayes shrinkage toward the
// global-cohort median. When the org has no data at all it falls back to the
// fixed DefaultSecsForDifficulty prior.
//
// Must be safe to call cold (no rows) — it never errors on "no data", only on
// real DB failures. Runs its reads inside db.WithOrg (RLS).
func CalibratedSecs(ctx context.Context, database *db.DB, orgID string, difficulty float64, cohortCandidates []string) (CalibratedResult, error) {
	bucket := DifficultyBucket(difficulty)

	var rows map[string]store.CalibrationCell
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		keys := append(append([]string{}, cohortCandidates...), GlobalCohort)
		m, err := store.GetCalibrationCells(ctx, tx, orgID, keys, bucket)
		rows = m
		return err
	}); err != nil {
		return CalibratedResult{}, err
	}

	globalMedian, hasGlobal := 0.0, false
	if g, ok := rows[GlobalCohort]; ok && g.N > 0 && g.MedianSecs > 0 {
		globalMedian, hasGlobal = float64(g.MedianSecs), true
	}

	prior := DefaultSecsForDifficulty(difficulty)

	// Walk richest-first; the first cohort with enough samples wins outright.
	for _, key := range cohortCandidates {
		cell, ok := rows[key]
		if !ok || cell.N <= 0 || cell.MedianSecs <= 0 {
			continue
		}
		cohortMedian := float64(cell.MedianSecs)

		if cell.N >= MinCohortN {
			return CalibratedResult{PredictedSecs: cohortMedian, CohortKey: key}, nil
		}

		// Sparse cohort: empirical-Bayes shrink toward the prior. Use the global
		// median as the prior when available, else the fixed curve.
		priorVal := prior
		if hasGlobal && key != GlobalCohort {
			priorVal = globalMedian
		}
		est := ShrinkToPrior(cohortMedian, priorVal, cell.N, ShrinkageK)
		return CalibratedResult{PredictedSecs: est, CohortKey: key, Shrunk: true}, nil
	}

	// No usable cohort row matched the candidates; fall back to global if present.
	if hasGlobal {
		return CalibratedResult{PredictedSecs: globalMedian, CohortKey: GlobalCohort}, nil
	}

	// True cold start: fixed prior.
	return CalibratedResult{PredictedSecs: prior, CohortKey: GlobalCohort, ColdStart: true}, nil
}
