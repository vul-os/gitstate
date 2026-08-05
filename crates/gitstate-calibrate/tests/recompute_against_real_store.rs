//! End-to-end fidelity check: seeds a real `SqliteStore` (the same one
//! `gitstate-daemon`/`gitstate-cli` use) with merged PRs, runs
//! `recompute_calibration` against it, and asserts the resulting curve/
//! accuracy numbers by hand-computed expectation — not just "it ran without
//! erroring". This is the proof that the pure math in `curve`/`recompute`
//! composes correctly with the real persistence layer's SQL (joins, the
//! dynamic `IN (...)` clause, `ON CONFLICT` upserts), none of which the
//! pure-function unit tests in `curve.rs`/`cohort.rs`/`recompute.rs` exercise.

use gitstate_calibrate::recompute::recompute_calibration;
use gitstate_core::{
    EffortEstimate, EffortMethod, Forge, Repo, RepoId, Store, WorkItem, WorkItemId, WorkKind,
    WorkState,
};
use gitstate_store::SqliteStore;

fn seed_repo(store: &SqliteStore) -> RepoId {
    let repo = Repo {
        id: RepoId::new(),
        slug: "acme/widgets".into(),
        path: "/tmp/widgets".into(),
        remote_url: None,
        forge: Forge::GitHub,
        default_branch: "main".into(),
        last_scanned_at: None,
        added_at: "2026-01-01T00:00:00Z".into(),
    };
    store.upsert_repo(&repo).unwrap();
    repo.id
}

fn merged_pr(repo_id: &RepoId, external_ref: &str, created_at: &str, merged_at: &str) -> WorkItem {
    WorkItem {
        id: WorkItemId::new(),
        repo_id: repo_id.clone(),
        kind: WorkKind::Pr,
        external_ref: external_ref.into(),
        title: "does a thing".into(),
        body: String::new(),
        state: WorkState::Merged,
        author_login: Some("dev".into()),
        labels: vec![],
        created_at: created_at.into(),
        updated_at: merged_at.into(),
        merged_at: Some(merged_at.into()),
        closed_at: Some(merged_at.into()),
        files_touched: vec![],
    }
}

/// Two merged PRs in the same cohort, same difficulty bucket, both merged at
/// the SAME instant (so recency weighting is a no-op and the expected
/// quantiles/accuracy numbers can be hand-computed exactly, with no
/// exponential-decay arithmetic to reproduce in the assertion).
#[test]
fn recompute_produces_hand_computed_curve_and_accuracy_numbers() {
    let store = SqliteStore::open_in_memory().unwrap();
    let repo_id = seed_repo(&store);

    // PR1: created -> merged is exactly 1000s. PR2: exactly 2000s. Both
    // merged at the same wall-clock instant, both judged at difficulty 5.0.
    let pr1 = merged_pr(
        &repo_id,
        "#1",
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:16:40Z",
    );
    let pr2 = merged_pr(
        &repo_id,
        "#2",
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:33:20Z",
    );
    store.save_work_items(&[pr1.clone(), pr2.clone()]).unwrap();

    for item in [&pr1, &pr2] {
        store
            .save_effort(&[EffortEstimate {
                item_id: item.id.clone(),
                difficulty: 5.0,
                method: EffortMethod::Heuristic,
                rationale: "test fixture".into(),
                confidence: 0.9,
            }])
            .unwrap();
        // Both estimates are assigned the same cohort and the same
        // predicted_secs (1500), so the expected MAE/bias below are exact:
        // |1500-1000|=500, |1500-2000|=500 -> MAE=500;
        // 1500/1000=1.5, 1500/2000=0.75 -> mean bias=1.125.
        store
            .update_effort_calibration(&item.id, 1500.0, "repo:acme/widgets", "s", "feature")
            .unwrap();
    }

    // Backfill actual_secs from the merged PRs' created_at/merged_at gap.
    let backfilled = store.backfill_actual_secs().unwrap();
    assert_eq!(
        backfilled, 2,
        "both merged PRs should get a fresh actual_secs"
    );

    // Recompute at the exact merge instant, so recency weight = 1 for both
    // samples and the weighted quantiles collapse to the classic (unweighted)
    // quantiles computed by hand in curve.rs's own equal-weight test.
    let now = time::OffsetDateTime::parse(
        "2026-01-01T00:33:20Z",
        &time::format_description::well_known::Rfc3339,
    )
    .unwrap()
    .unix_timestamp() as f64;

    let summary = recompute_calibration(&store, now).unwrap();
    assert_eq!(
        summary.actuals_backfilled, 0,
        "recompute's OWN backfill call is idempotent"
    );
    assert_eq!(summary.samples, 2);
    assert_eq!(summary.outcomes, 2);

    // The cohort was stored as "repo:acme/widgets" (no "|area:" segment), so
    // `expand_cohorts` widens it to {global, repo:acme/widgets} — both should
    // carry identical curve cells since every sample belongs to both.
    for cohort in ["repo:acme/widgets", "global"] {
        let cells = store
            .get_calibration_cells(&[cohort.to_string()], 5)
            .unwrap();
        let cell = cells
            .get(cohort)
            .unwrap_or_else(|| panic!("missing cell for {cohort}"));
        assert_eq!(cell.n, 2, "cohort {cohort}");
        assert_eq!(cell.p25_secs, 1000, "cohort {cohort} p25");
        assert_eq!(cell.median_secs, 1500, "cohort {cohort} median");
        assert_eq!(cell.p75_secs, 2000, "cohort {cohort} p75");
        assert_eq!(cell.mean_secs, 1500, "cohort {cohort} mean");
    }

    // Same widening applies to the accuracy summary.
    let accuracy = store.list_accuracy().unwrap();
    assert_eq!(accuracy.len(), 2, "repo cohort + global");
    for row in &accuracy {
        assert_eq!(row.n, 2, "cohort {}", row.cohort_key);
        assert_eq!(row.mae_secs, 500, "cohort {}", row.cohort_key);
        assert!(
            (row.bias_ratio - 1.125).abs() < 1e-9,
            "cohort {} bias_ratio = {}",
            row.cohort_key,
            row.bias_ratio
        );
    }

    // The read path (`calibrated_secs`) should now pick the repo cohort
    // outright: n=2 < MIN_COHORT_N(5), so it shrinks toward the global
    // median (also 1500 here) rather than using the raw cohort median blind.
    let result =
        gitstate_calibrate::curve::calibrated_secs(&store, 5.0, &["repo:acme/widgets".to_string()])
            .unwrap();
    assert_eq!(result.cohort_key, "repo:acme/widgets");
    assert!(result.shrunk, "n=2 is below MIN_COHORT_N, must shrink");
    // shrink_to_prior(1500, 1500, 2, 8) == 1500 regardless of n/k, since
    // cohort and prior already agree exactly.
    assert!((result.predicted_secs - 1500.0).abs() < 1e-6);
}

/// Cold start: an empty store has no calibration data at all, so the read
/// path must fall back to the fixed difficulty->seconds prior rather than
/// erroring on "no rows".
#[test]
fn calibrated_secs_cold_starts_on_an_empty_store() {
    let store = SqliteStore::open_in_memory().unwrap();
    let result =
        gitstate_calibrate::curve::calibrated_secs(&store, 5.0, &["repo:nothing-here".to_string()])
            .unwrap();
    assert!(result.cold_start);
    assert_eq!(result.cohort_key, "global");
    assert!(
        (result.predicted_secs - gitstate_calibrate::curve::default_secs_for_difficulty(5.0)).abs()
            < 1e-9
    );
}
