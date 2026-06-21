// Package store — calibration.go
// Org-scoped accessors for the effort-calibration tables (migration 017):
//   - effort_calibration: per-cohort, per-difficulty-bucket difficulty→time curve
//   - effort_accuracy:    per-cohort MAE / bias summary
//
// Plus the read/write helpers on effort_estimates' new columns (predicted_secs,
// actual_secs, cohort_key, size_bucket, change_type).
//
// All writes/reads run inside db.WithOrg (pgx.Tx) so the non-superuser FORCE-RLS
// role sees app.current_org; a bare-pool read returns ZERO rows.
package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── effort_calibration ─────────────────────────────────────────────────────────

// CalibrationCell is one (cohort_key, difficulty_bucket) row of the curve.
type CalibrationCell struct {
	CohortKey        string
	DifficultyBucket int
	MedianSecs       int64
	P25Secs          int64
	P75Secs          int64
	MeanSecs         int64
	N                int
	UpdatedAt        time.Time
}

// UpsertCalibrationCell inserts or replaces one curve cell for the org.
func UpsertCalibrationCell(ctx context.Context, tx pgx.Tx, orgID string, c CalibrationCell) error {
	const q = `
		INSERT INTO effort_calibration
			(org_id, cohort_key, difficulty_bucket,
			 median_secs, p25_secs, p75_secs, mean_secs, n, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (org_id, cohort_key, difficulty_bucket) DO UPDATE SET
			median_secs = EXCLUDED.median_secs,
			p25_secs    = EXCLUDED.p25_secs,
			p75_secs    = EXCLUDED.p75_secs,
			mean_secs   = EXCLUDED.mean_secs,
			n           = EXCLUDED.n,
			updated_at  = now()`
	_, err := tx.Exec(ctx, q,
		orgID, c.CohortKey, c.DifficultyBucket,
		c.MedianSecs, c.P25Secs, c.P75Secs, c.MeanSecs, c.N)
	if err != nil {
		return fmt.Errorf("store.calibration: upsert cell %s/%d: %w", c.CohortKey, c.DifficultyBucket, err)
	}
	return nil
}

// GetCalibrationCells returns the cells for the given cohort keys at one
// difficulty bucket, keyed by cohort_key. Missing cohorts are simply absent from
// the map (no error). MUST run inside db.WithOrg.
func GetCalibrationCells(ctx context.Context, qr Querier, orgID string, cohortKeys []string, bucket int) (map[string]CalibrationCell, error) {
	out := map[string]CalibrationCell{}
	if len(cohortKeys) == 0 {
		return out, nil
	}
	const q = `
		SELECT cohort_key, difficulty_bucket,
		       COALESCE(median_secs,0), COALESCE(p25_secs,0),
		       COALESCE(p75_secs,0), COALESCE(mean_secs,0),
		       n, updated_at
		FROM effort_calibration
		WHERE org_id = $1 AND difficulty_bucket = $2 AND cohort_key = ANY($3)`
	rows, err := qr.Query(ctx, q, orgID, bucket, cohortKeys)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: get cells: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c CalibrationCell
		if err := rows.Scan(&c.CohortKey, &c.DifficultyBucket,
			&c.MedianSecs, &c.P25Secs, &c.P75Secs, &c.MeanSecs,
			&c.N, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan cell: %w", err)
		}
		out[c.CohortKey] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.calibration: get cells rows: %w", err)
	}
	return out, nil
}

// ListCalibration returns all curve cells for the org (for the API curve view),
// ordered by cohort then bucket. MUST run inside db.WithOrg.
func ListCalibration(ctx context.Context, qr Querier, orgID string) ([]CalibrationCell, error) {
	const q = `
		SELECT cohort_key, difficulty_bucket,
		       COALESCE(median_secs,0), COALESCE(p25_secs,0),
		       COALESCE(p75_secs,0), COALESCE(mean_secs,0),
		       n, updated_at
		FROM effort_calibration
		WHERE org_id = $1
		ORDER BY cohort_key, difficulty_bucket`
	rows, err := qr.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: list: %w", err)
	}
	defer rows.Close()
	var out []CalibrationCell
	for rows.Next() {
		var c CalibrationCell
		if err := rows.Scan(&c.CohortKey, &c.DifficultyBucket,
			&c.MedianSecs, &c.P25Secs, &c.P75Secs, &c.MeanSecs,
			&c.N, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan list cell: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── effort_accuracy ─────────────────────────────────────────────────────────────

// AccuracyRow is the per-cohort estimation-accuracy summary.
type AccuracyRow struct {
	CohortKey string
	N         int
	MAESecs   int64
	BiasRatio float64 // mean(predicted/actual); <1 ⇒ under-estimating
	UpdatedAt time.Time
}

// UpsertAccuracy inserts or replaces the per-cohort accuracy summary.
func UpsertAccuracy(ctx context.Context, tx pgx.Tx, orgID string, a AccuracyRow) error {
	const q = `
		INSERT INTO effort_accuracy (org_id, cohort_key, n, mae_secs, bias_ratio, updated_at)
		VALUES ($1,$2,$3,$4,$5, now())
		ON CONFLICT (org_id, cohort_key) DO UPDATE SET
			n          = EXCLUDED.n,
			mae_secs   = EXCLUDED.mae_secs,
			bias_ratio = EXCLUDED.bias_ratio,
			updated_at = now()`
	_, err := tx.Exec(ctx, q, orgID, a.CohortKey, a.N, a.MAESecs, a.BiasRatio)
	if err != nil {
		return fmt.Errorf("store.calibration: upsert accuracy %s: %w", a.CohortKey, err)
	}
	return nil
}

// ListAccuracy returns all accuracy rows for the org, most-sampled first.
func ListAccuracy(ctx context.Context, qr Querier, orgID string) ([]AccuracyRow, error) {
	const q = `
		SELECT cohort_key, n, COALESCE(mae_secs,0), COALESCE(bias_ratio,0), updated_at
		FROM effort_accuracy
		WHERE org_id = $1
		ORDER BY n DESC, cohort_key`
	rows, err := qr.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: list accuracy: %w", err)
	}
	defer rows.Close()
	var out []AccuracyRow
	for rows.Next() {
		var a AccuracyRow
		if err := rows.Scan(&a.CohortKey, &a.N, &a.MAESecs, &a.BiasRatio, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan accuracy: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── effort_estimates calibration columns ────────────────────────────────────────

// EstimateOutcome is one merged-PR estimate joined to its observed actual time.
// Used to build the curves (predicted/actual may be NULL until backfilled).
type EstimateOutcome struct {
	EstimateID   string
	PRID         string
	CohortKey    string
	Difficulty   float64
	PredictedSec *float64
	ActualSecs   *int64
	MergedAt     time.Time
}

// BackfillActualSecs fills effort_estimates.actual_secs from cycle_times
// (lead_time_secs) for merged PRs whose estimate has no actual yet. Returns the
// number of rows updated. MUST run inside db.WithOrg.
//
// It joins to the LATEST cycle_time per PR (max computed_at) so re-measurements
// don't double-count.
func BackfillActualSecs(ctx context.Context, tx pgx.Tx, orgID string) (int64, error) {
	const q = `
		WITH latest_ct AS (
			SELECT DISTINCT ON (pr_id) pr_id, lead_time_secs
			FROM cycle_times
			WHERE org_id = $1 AND pr_id IS NOT NULL AND lead_time_secs IS NOT NULL
			ORDER BY pr_id, computed_at DESC
		)
		UPDATE effort_estimates ee
		SET actual_secs = lc.lead_time_secs
		FROM latest_ct lc
		JOIN pull_requests pr ON pr.id = lc.pr_id
		WHERE ee.org_id = $1
		  AND ee.pr_id = lc.pr_id
		  AND pr.state = 'merged'
		  AND ee.actual_secs IS DISTINCT FROM lc.lead_time_secs`
	tag, err := tx.Exec(ctx, q, orgID)
	if err != nil {
		return 0, fmt.Errorf("store.calibration: backfill actuals: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListEstimateOutcomes returns the latest estimate per PR that has a non-null
// cohort_key and actual_secs (i.e. ready to feed the curve), with the PR's
// merged_at for recency weighting. MUST run inside db.WithOrg.
func ListEstimateOutcomes(ctx context.Context, qr Querier, orgID string) ([]EstimateOutcome, error) {
	const q = `
		SELECT DISTINCT ON (ee.pr_id)
		       ee.id, ee.pr_id::text,
		       COALESCE(ee.cohort_key, ''),
		       ee.difficulty::float8,
		       ee.predicted_secs::float8,
		       ee.actual_secs,
		       pr.merged_at
		FROM effort_estimates ee
		JOIN pull_requests pr ON pr.id = ee.pr_id
		WHERE ee.org_id = $1
		  AND ee.pr_id IS NOT NULL
		  AND ee.actual_secs IS NOT NULL
		  AND pr.merged_at IS NOT NULL
		ORDER BY ee.pr_id, ee.created_at DESC`
	rows, err := qr.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: list outcomes: %w", err)
	}
	defer rows.Close()
	var out []EstimateOutcome
	for rows.Next() {
		var o EstimateOutcome
		var pred *float64
		if err := rows.Scan(&o.EstimateID, &o.PRID, &o.CohortKey,
			&o.Difficulty, &pred, &o.ActualSecs, &o.MergedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan outcome: %w", err)
		}
		o.PredictedSec = pred
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateEstimateCalibration persists the calibration fields onto an existing
// effort_estimates row. MUST run inside db.WithOrg.
func UpdateEstimateCalibration(ctx context.Context, tx pgx.Tx, orgID, estimateID string, predictedSecs float64, cohortKey, sizeBucket, changeType string) error {
	const q = `
		UPDATE effort_estimates
		SET predicted_secs = $3, cohort_key = $4, size_bucket = $5, change_type = $6
		WHERE org_id = $1 AND id = $2`
	_, err := tx.Exec(ctx, q, orgID, estimateID, predictedSecs, cohortKey, sizeBucket, changeType)
	if err != nil {
		return fmt.Errorf("store.calibration: update estimate calibration: %w", err)
	}
	return nil
}

// SampleRow couples a raw (cohort_key, difficulty, actual_secs, merged_at) tuple
// for curve recomputation. Returned ungrouped so the Go layer can apply recency
// weighting per (cohort,bucket).
type SampleRow struct {
	CohortKey  string
	Difficulty float64
	ActualSecs int64
	MergedAt   time.Time
}

// ListCohortSamples returns every merged-PR outcome that has a cohort_key and an
// actual, expanded so EACH cohort candidate the estimate belongs to could be
// rebuilt. We store only the chosen cohort_key per estimate; the recompute layer
// derives the richer/coarser keys from it. MUST run inside db.WithOrg.
func ListCohortSamples(ctx context.Context, qr Querier, orgID string) ([]SampleRow, error) {
	const q = `
		SELECT DISTINCT ON (ee.pr_id)
		       COALESCE(ee.cohort_key,''), ee.difficulty::float8,
		       ee.actual_secs, pr.merged_at
		FROM effort_estimates ee
		JOIN pull_requests pr ON pr.id = ee.pr_id
		WHERE ee.org_id = $1
		  AND ee.pr_id IS NOT NULL
		  AND ee.actual_secs IS NOT NULL
		  AND pr.merged_at IS NOT NULL
		ORDER BY ee.pr_id, ee.created_at DESC`
	rows, err := qr.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: list samples: %w", err)
	}
	defer rows.Close()
	var out []SampleRow
	for rows.Next() {
		var s SampleRow
		if err := rows.Scan(&s.CohortKey, &s.Difficulty, &s.ActualSecs, &s.MergedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── exemplars ───────────────────────────────────────────────────────────────────

// Exemplar is a past merged PR in a cohort, used as a prompt anchor.
type Exemplar struct {
	PRTitle      string
	Difficulty   float64
	PredictedSec *float64
	ActualSecs   *int64
}

// ListExemplars returns up to `limit` recent merged PRs in the given cohort_key
// that have BOTH a predicted and an actual, newest-first. MUST run inside
// db.WithOrg.
func ListExemplars(ctx context.Context, qr Querier, orgID, cohortKey string, limit int) ([]Exemplar, error) {
	if limit <= 0 {
		limit = 3
	}
	const q = `
		SELECT DISTINCT ON (ee.pr_id)
		       COALESCE(pr.title,''), ee.difficulty::float8,
		       ee.predicted_secs::float8, ee.actual_secs, pr.merged_at
		FROM effort_estimates ee
		JOIN pull_requests pr ON pr.id = ee.pr_id
		WHERE ee.org_id = $1
		  AND ee.cohort_key = $2
		  AND ee.actual_secs IS NOT NULL
		  AND ee.predicted_secs IS NOT NULL
		ORDER BY ee.pr_id, ee.created_at DESC`
	rows, err := qr.Query(ctx, q, orgID, cohortKey)
	if err != nil {
		return nil, fmt.Errorf("store.calibration: list exemplars: %w", err)
	}
	defer rows.Close()
	type rec struct {
		ex       Exemplar
		mergedAt time.Time
	}
	var recs []rec
	for rows.Next() {
		var ex Exemplar
		var pred *float64
		var mergedAt time.Time
		if err := rows.Scan(&ex.PRTitle, &ex.Difficulty, &pred, &ex.ActualSecs, &mergedAt); err != nil {
			return nil, fmt.Errorf("store.calibration: scan exemplar: %w", err)
		}
		ex.PredictedSec = pred
		recs = append(recs, rec{ex: ex, mergedAt: mergedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// DISTINCT ON forces ordering by pr_id; re-sort by recency (newest first) and cap.
	sort.Slice(recs, func(i, j int) bool { return recs[i].mergedAt.After(recs[j].mergedAt) })
	out := make([]Exemplar, 0, limit)
	for i, r := range recs {
		if i >= limit {
			break
		}
		out = append(out, r.ex)
	}
	return out, nil
}
