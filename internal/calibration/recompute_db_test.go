package calibration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/calibration"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// TestRecomputeCalibrationRoundTrip seeds an org with merged PRs + cycle_times +
// effort_estimates, runs RecomputeCalibration, and asserts the curve is built,
// actuals are backfilled, accuracy is summarised, and CalibratedSecs reads it
// back (direct cohort, shrinkage, and cold-start paths).
//
// It commits real rows (RecomputeCalibration commits its own tx) and cleans up
// by deleting the throwaway org (ON DELETE CASCADE removes all children).
func TestRecomputeCalibrationRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping calibration round-trip integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ns := time.Now().UnixNano()
	pool := database.Pool()

	// Create a throwaway org (organizations has no RLS).
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("calib-%d", ns), "Calib Test").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Seed everything under the org's RLS context in ONE committed tx.
	var repoID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name)
			 VALUES ($1,'github',$2,'acme/app') RETURNING id`,
			orgID, fmt.Sprintf("calib-repo-%d", ns)).Scan(&repoID); err != nil {
			return err
		}

		// Cohort under test: repo:<id>|area:internal at difficulty bucket 5.
		// Seed 6 merged PRs (≥ MinCohortN) with a tight actual cluster around
		// 36000s (10h) so the cohort is used DIRECTLY (no shrinkage).
		cohort := fmt.Sprintf("repo:%s|area:internal", repoID)
		actuals := []int64{30000, 33000, 36000, 36000, 39000, 42000}
		for i, secs := range actuals {
			var prID string
			if err := tx.QueryRow(ctx,
				`INSERT INTO pull_requests
				   (org_id, repo_id, platform, external_id, number, title, state, merged_at)
				 VALUES ($1,$2,'github',$3,$4,$5,'merged',$6) RETURNING id`,
				orgID, repoID, fmt.Sprintf("calib-pr-%d-%d", ns, i), i+1,
				fmt.Sprintf("feat: thing %d", i), now.Add(-time.Duration(i)*24*time.Hour),
			).Scan(&prID); err != nil {
				return err
			}
			// cycle_times carries the observed lead time (actual).
			if _, err := tx.Exec(ctx,
				`INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, computed_at)
				 VALUES ($1,$2,$3, now())`, orgID, prID, secs); err != nil {
				return err
			}
			// effort_estimate: difficulty 5, a deliberately-LOW prediction (half the
			// actual) so bias_ratio < 1 (under-estimating). actual_secs left NULL —
			// RecomputeCalibration must backfill it from cycle_times.
			predicted := float64(secs) / 2
			if _, err := tx.Exec(ctx,
				`INSERT INTO effort_estimates
				   (org_id, pr_id, difficulty, predicted_secs, cohort_key, size_bucket, change_type)
				 VALUES ($1,$2,5,$3,$4,'m','feature')`,
				orgID, prID, predicted, cohort); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// ── Run the closed loop ──────────────────────────────────────────────────
	if err := calibration.RecomputeCalibration(ctx, database, orgID, now); err != nil {
		t.Fatalf("RecomputeCalibration: %v", err)
	}

	// (a) actuals backfilled onto effort_estimates.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var nNull int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM effort_estimates WHERE org_id=$1 AND actual_secs IS NULL`,
			orgID).Scan(&nNull); err != nil {
			return err
		}
		if nNull != 0 {
			t.Errorf("expected all actuals backfilled, %d still NULL", nNull)
		}
		return nil
	}); err != nil {
		t.Fatalf("check backfill: %v", err)
	}

	// (b) curve cells exist for the repo|area cohort AND the rolled-up global.
	var cells map[string]store.CalibrationCell
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		cells, e = store.GetCalibrationCells(ctx, tx, orgID,
			[]string{fmt.Sprintf("repo:%s|area:internal", repoID), calibration.GlobalCohort}, 5)
		return e
	}); err != nil {
		t.Fatalf("get cells: %v", err)
	}
	areaCell, ok := cells[fmt.Sprintf("repo:%s|area:internal", repoID)]
	if !ok {
		t.Fatal("expected repo|area cohort cell at bucket 5")
	}
	if areaCell.N != 6 {
		t.Errorf("area cell n: want 6, got %d", areaCell.N)
	}
	// Recency-weighted median should land near the recent cluster (~33000) — the
	// most-recent (heaviest) PRs carry the smaller actuals.
	if areaCell.MedianSecs < 30000 || areaCell.MedianSecs > 42000 {
		t.Errorf("area median out of range: %d", areaCell.MedianSecs)
	}
	if _, ok := cells[calibration.GlobalCohort]; !ok {
		t.Error("expected rolled-up global cohort cell at bucket 5")
	}

	// (c) accuracy: predicted = actual/2 ⇒ bias_ratio ≈ 0.5 (under-estimating).
	var accs []store.AccuracyRow
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		accs, e = store.ListAccuracy(ctx, tx, orgID)
		return e
	}); err != nil {
		t.Fatalf("list accuracy: %v", err)
	}
	var globalAcc *store.AccuracyRow
	for i := range accs {
		if accs[i].CohortKey == calibration.GlobalCohort {
			globalAcc = &accs[i]
		}
	}
	if globalAcc == nil {
		t.Fatal("expected global accuracy row")
	}
	if globalAcc.BiasRatio < 0.45 || globalAcc.BiasRatio > 0.55 {
		t.Errorf("bias_ratio want ≈0.5 (under-estimating), got %.3f", globalAcc.BiasRatio)
	}
	if globalAcc.MAESecs <= 0 {
		t.Errorf("mae_secs want > 0, got %d", globalAcc.MAESecs)
	}

	// ── CalibratedSecs read-back ─────────────────────────────────────────────

	// Direct cohort hit (n=6 ≥ MinCohortN): returns the area median, no shrink.
	candidates := []string{fmt.Sprintf("repo:%s|area:internal", repoID), fmt.Sprintf("repo:%s", repoID), "type:feature", calibration.GlobalCohort}
	res, err := calibration.CalibratedSecs(ctx, database, orgID, 5, candidates)
	if err != nil {
		t.Fatalf("CalibratedSecs direct: %v", err)
	}
	if res.CohortKey != fmt.Sprintf("repo:%s|area:internal", repoID) {
		t.Errorf("direct cohort want area, got %q", res.CohortKey)
	}
	if res.Shrunk || res.ColdStart {
		t.Errorf("direct hit should not shrink/cold-start: %+v", res)
	}
	if res.PredictedSecs != float64(areaCell.MedianSecs) {
		t.Errorf("direct predicted want %d, got %.1f", areaCell.MedianSecs, res.PredictedSecs)
	}

	// Cold-start path: a difficulty bucket with no data ⇒ fixed prior.
	cold, err := calibration.CalibratedSecs(ctx, database, orgID, 9, candidates)
	if err != nil {
		t.Fatalf("CalibratedSecs cold: %v", err)
	}
	if !cold.ColdStart {
		t.Errorf("bucket 9 has no data; want ColdStart, got %+v", cold)
	}
	if cold.PredictedSecs != calibration.DefaultSecsForDifficulty(9) {
		t.Errorf("cold predicted want default %.0f, got %.0f",
			calibration.DefaultSecsForDifficulty(9), cold.PredictedSecs)
	}
}
