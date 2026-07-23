package calibration

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RecomputeCalibration is the closed loop. Inside one db.WithOrg tx it:
//
//	(a) backfills effort_estimates.actual_secs from cycle_times (lead time) for
//	    merged PRs;
//	(b) for each (cohort_key, difficulty_bucket) builds a recency-weighted
//	    median/p25/p75/mean + n over the org's history and upserts
//	    effort_calibration;
//	(c) computes per-cohort effort_accuracy (MAE + bias_ratio) and upserts it.
//
// It is idempotent and safe to call after every sync. `now` is injected for
// deterministic tests; pass time.Now() in production.
func RecomputeCalibration(ctx context.Context, database *db.DB, orgID string, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	return database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// (a) backfill actuals from observed lead times.
		filled, err := store.BackfillActualSecs(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("calibration.Recompute: backfill: %w", err)
		}

		// (b) build the curves.
		samples, err := store.ListCohortSamples(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("calibration.Recompute: list samples: %w", err)
		}
		if err := buildCurves(ctx, tx, orgID, samples, now); err != nil {
			return err
		}

		// (c) per-cohort accuracy.
		outcomes, err := store.ListEstimateOutcomes(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("calibration.Recompute: list outcomes: %w", err)
		}
		if err := buildAccuracy(ctx, tx, orgID, outcomes); err != nil {
			return err
		}

		slog.Info("calibration.Recompute: done",
			"org_id", orgID, "actuals_backfilled", filled,
			"samples", len(samples), "outcomes", len(outcomes))
		return nil
	})
}

// expandCohorts returns every cohort level a stored richest cohort_key belongs
// to, so a single estimate feeds repo|area, repo, type, AND global curves. The
// type cohort cannot be reconstructed from a repo-style key, so we only re-derive
// the coarser keys that are PREFIXES of the stored key (plus global, always).
//
//	repo:R|area:A → {repo:R|area:A, repo:R, global}
//	repo:R        → {repo:R, global}
//	type:T        → {type:T, global}
//	global / ""   → {global}
func expandCohorts(stored string) []string {
	out := []string{GlobalCohort}
	stored = strings.TrimSpace(stored)
	switch {
	case stored == "" || stored == GlobalCohort:
		// only global
	case strings.HasPrefix(stored, "repo:"):
		if i := strings.Index(stored, "|area:"); i >= 0 {
			repoKey := stored[:i]
			out = append(out, stored, repoKey)
		} else {
			out = append(out, stored)
		}
	default: // type:… or any other single-segment key
		out = append(out, stored)
	}
	return out
}

// curveKey groups samples by (cohort_key, difficulty_bucket).
type curveKey struct {
	cohort string
	bucket int
}

// buildCurves groups every sample into all the cohort levels it belongs to and
// upserts a recency-weighted calibration cell per (cohort, difficulty_bucket).
func buildCurves(ctx context.Context, tx pgx.Tx, orgID string, samples []store.SampleRow, now time.Time) error {
	groups := map[curveKey][]Sample{}
	for _, s := range samples {
		// Drop non-positive lead times. ListCohortSamples only filters
		// actual_secs IS NOT NULL, so a zero/negative duration (e.g. clock
		// skew → merged_at before the first commit) can slip through. The
		// accuracy half of the recompute (buildAccuracy) already excludes
		// actual_secs <= 0; the curve half must do the same or the two halves
		// disagree and a spurious 0s sample drags median/p25/p75 toward zero.
		if s.ActualSecs <= 0 {
			continue
		}
		bucket := DifficultyBucket(s.Difficulty)
		smp := Sample{ActualSecs: s.ActualSecs, MergedAt: s.MergedAt}
		for _, cohort := range expandCohorts(s.CohortKey) {
			k := curveKey{cohort: cohort, bucket: bucket}
			groups[k] = append(groups[k], smp)
		}
	}

	for k, smps := range groups {
		p25, median, p75, mean, n, ok := WeightedQuantiles(smps, now, RecencyHalfLife)
		if !ok {
			continue
		}
		cell := store.CalibrationCell{
			CohortKey:        k.cohort,
			DifficultyBucket: k.bucket,
			MedianSecs:       roundSecs(median),
			P25Secs:          roundSecs(p25),
			P75Secs:          roundSecs(p75),
			MeanSecs:         roundSecs(mean),
			N:                n,
		}
		if err := store.UpsertCalibrationCell(ctx, tx, orgID, cell); err != nil {
			return fmt.Errorf("calibration.Recompute: upsert cell: %w", err)
		}
	}
	return nil
}

// buildAccuracy computes MAE and bias_ratio per cohort level (including global)
// from estimates that have BOTH a prediction and an actual.
func buildAccuracy(ctx context.Context, tx pgx.Tx, orgID string, outcomes []store.EstimateOutcome) error {
	type acc struct {
		n       int
		absErr  float64
		biasSum float64 // sum of predicted/actual
	}
	groups := map[string]*acc{}
	for _, o := range outcomes {
		if o.PredictedSec == nil || o.ActualSecs == nil || *o.ActualSecs <= 0 {
			continue
		}
		pred := *o.PredictedSec
		actual := float64(*o.ActualSecs)
		for _, cohort := range expandCohorts(o.CohortKey) {
			a := groups[cohort]
			if a == nil {
				a = &acc{}
				groups[cohort] = a
			}
			a.n++
			a.absErr += math.Abs(pred - actual)
			a.biasSum += pred / actual
		}
	}

	for cohort, a := range groups {
		if a.n == 0 {
			continue
		}
		row := store.AccuracyRow{
			CohortKey: cohort,
			N:         a.n,
			MAESecs:   roundSecs(a.absErr / float64(a.n)),
			BiasRatio: a.biasSum / float64(a.n),
		}
		if err := store.UpsertAccuracy(ctx, tx, orgID, row); err != nil {
			return fmt.Errorf("calibration.Recompute: upsert accuracy: %w", err)
		}
	}
	return nil
}

func roundSecs(f float64) int64 {
	if f < 0 {
		return 0
	}
	return int64(math.Round(f))
}
