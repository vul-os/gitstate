//! The closed loop: backfill observed actuals, rebuild the per-cohort curves,
//! and recompute the per-cohort accuracy summary.
//!
//! Ported from Go's `internal/calibration/recompute.go`. Two persistence
//! differences from the Go reference, both directed by `docs/PORT-PLAN.md`
//! §1/§2 rather than invented here:
//!
//! - Go's `BackfillActualSecs` reads a persisted Postgres `cycle_times`
//!   table. Rust has no such table — `gitstate_core::analytics::cycle_times`
//!   already derives the same signal live from `work_items`, so
//!   [`Store::backfill_actual_secs`] computes lead time directly from
//!   `merged_at - created_at` (in whole seconds, not routed through
//!   `analytics::cycle_times`'s `f64` hours — see that trait method's doc
//!   for why staying in integer seconds avoids a needless float round-trip).
//! - `org_id` is dropped everywhere (single tenant, nothing to scope).
use gitstate_core::{CalibrationCell, Result, Store};

use crate::cohort::GLOBAL_COHORT;
use crate::curve::{difficulty_bucket, weighted_quantiles, Sample};

/// Returns every cohort level a stored richest `cohort_key` belongs to, so a
/// single estimate feeds `repo|area`, `repo`, `type`, AND `global` curves.
/// The `type` cohort cannot be reconstructed from a repo-style key, so only
/// the coarser keys that are PREFIXES of the stored key (plus `global`,
/// always) are re-derived.
///
/// ```text
/// repo:R|area:A -> {repo:R|area:A, repo:R, global}
/// repo:R        -> {repo:R, global}
/// type:T        -> {type:T, global}
/// global / ""   -> {global}
/// ```
///
/// Ported verbatim from Go's `expandCohorts` in `recompute.go` — note this
/// is intentionally a *different* algorithm from `cohort::cohort_candidates`
/// (which derives candidates from fresh PR metadata); this one only ever
/// widens a key that was already chosen and stored.
pub fn expand_cohorts(stored: &str) -> Vec<String> {
    let mut out = vec![GLOBAL_COHORT.to_string()];
    let stored = stored.trim();
    if stored.is_empty() || stored == GLOBAL_COHORT {
        // only global
    } else if let Some(_rest) = stored.strip_prefix("repo:") {
        if let Some(i) = stored.find("|area:") {
            let repo_key = &stored[..i];
            out.push(stored.to_string());
            out.push(repo_key.to_string());
        } else {
            out.push(stored.to_string());
        }
    } else {
        // type:... or any other single-segment key
        out.push(stored.to_string());
    }
    out
}

/// Groups samples by (cohort_key, difficulty_bucket).
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct CurveKey {
    cohort: String,
    bucket: i64,
}

fn round_secs(f: f64) -> i64 {
    if f < 0.0 {
        0
    } else {
        f.round() as i64
    }
}

/// Groups every sample into all the cohort levels it belongs to and upserts
/// a recency-weighted calibration cell per (cohort, difficulty_bucket).
fn build_curves(
    store: &dyn Store,
    samples: &[gitstate_core::CohortSample],
    now_epoch_secs: f64,
) -> Result<()> {
    use std::collections::HashMap;
    let mut groups: HashMap<CurveKey, Vec<Sample>> = HashMap::new();
    for s in samples {
        // Drop non-positive lead times. `list_cohort_samples` only filters
        // `actual_secs IS NOT NULL`, so a zero/negative duration (e.g. clock
        // skew) can slip through. The accuracy half (`build_accuracy`)
        // already excludes `actual_secs <= 0`; the curve half must do the
        // same or the two halves disagree and a spurious 0s sample drags
        // median/p25/p75 toward zero.
        if s.actual_secs <= 0 {
            continue;
        }
        let bucket = difficulty_bucket(s.difficulty);
        let smp = Sample {
            actual_secs: s.actual_secs,
            merged_at_epoch_secs: parse_epoch_secs(&s.merged_at),
        };
        for cohort in expand_cohorts(&s.cohort_key) {
            groups
                .entry(CurveKey { cohort, bucket })
                .or_default()
                .push(smp);
        }
    }

    for (k, smps) in groups {
        let Some(q) =
            weighted_quantiles(&smps, now_epoch_secs, crate::curve::RECENCY_HALF_LIFE_SECS)
        else {
            continue;
        };
        let cell = CalibrationCell {
            cohort_key: k.cohort,
            difficulty_bucket: k.bucket,
            median_secs: round_secs(q.median),
            p25_secs: round_secs(q.p25),
            p75_secs: round_secs(q.p75),
            mean_secs: round_secs(q.mean),
            n: q.n as i64,
            updated_at: gitstate_core::now_rfc3339(),
        };
        store.upsert_calibration_cell(&cell)?;
    }
    Ok(())
}

/// Computes MAE and bias_ratio per cohort level (including global) from
/// outcomes that have BOTH a prediction and an actual.
fn build_accuracy(store: &dyn Store, outcomes: &[gitstate_core::EstimateOutcome]) -> Result<()> {
    use std::collections::HashMap;
    #[derive(Default)]
    struct Acc {
        n: i64,
        abs_err: f64,
        bias_sum: f64, // sum of predicted/actual
    }
    let mut groups: HashMap<String, Acc> = HashMap::new();
    for o in outcomes {
        let (Some(pred), Some(actual)) = (o.predicted_secs, o.actual_secs) else {
            continue;
        };
        if actual <= 0 {
            continue;
        }
        let actual = actual as f64;
        for cohort in expand_cohorts(&o.cohort_key) {
            let a = groups.entry(cohort).or_default();
            a.n += 1;
            a.abs_err += (pred - actual).abs();
            a.bias_sum += pred / actual;
        }
    }

    for (cohort, a) in groups {
        if a.n == 0 {
            continue;
        }
        let row = gitstate_core::AccuracyRow {
            cohort_key: cohort,
            n: a.n,
            mae_secs: round_secs(a.abs_err / a.n as f64),
            bias_ratio: a.bias_sum / a.n as f64,
            updated_at: gitstate_core::now_rfc3339(),
        };
        store.upsert_accuracy(&row)?;
    }
    Ok(())
}

/// Summary of one `recompute_calibration` pass, for logging (mirrors Go's
/// `slog.Info("calibration.Recompute: done", ...)`).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct RecomputeSummary {
    pub actuals_backfilled: u64,
    pub samples: usize,
    pub outcomes: usize,
}

/// The closed loop. In order:
///
/// 1. backfills `effort.actual_secs` from each merged PR's observed lead time;
/// 2. for each (cohort_key, difficulty_bucket) builds a recency-weighted
///    median/p25/p75/mean + n over this machine's history and upserts
///    `effort_calibration`;
/// 3. computes per-cohort `effort_accuracy` (MAE + bias_ratio) and upserts it.
///
/// Idempotent and safe to call after every sync. `now_epoch_secs` is
/// injected for deterministic tests; pass the real wall clock in production.
pub fn recompute_calibration(store: &dyn Store, now_epoch_secs: f64) -> Result<RecomputeSummary> {
    let actuals_backfilled = store.backfill_actual_secs()?;

    let samples = store.list_cohort_samples()?;
    build_curves(store, &samples, now_epoch_secs)?;

    let outcomes = store.list_estimate_outcomes()?;
    build_accuracy(store, &outcomes)?;

    Ok(RecomputeSummary {
        actuals_backfilled,
        samples: samples.len(),
        outcomes: outcomes.len(),
    })
}

/// Parses an RFC3339 timestamp into seconds since the Unix epoch, defaulting
/// to 0 (the epoch itself — the oldest possible instant, so an unparseable
/// timestamp is weighted as maximally stale rather than crashing the recompute)
/// on failure. Recompute already only sees timestamps this store itself wrote.
fn parse_epoch_secs(ts: &str) -> f64 {
    time::OffsetDateTime::parse(ts, &time::format_description::well_known::Rfc3339)
        .map(|t| t.unix_timestamp() as f64)
        .unwrap_or(0.0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn expand_cohorts_matches_go_fixtures() {
        // Ported verbatim from recompute_test... (Go's cohort_test.go
        // TestExpandCohorts — expandCohorts lives in recompute.go but is
        // tested alongside cohort.go's other pure functions).
        assert_eq!(
            expand_cohorts("repo:R1|area:internal"),
            vec!["global", "repo:R1|area:internal", "repo:R1"]
        );
        assert_eq!(expand_cohorts("repo:R1"), vec!["global", "repo:R1"]);
        assert_eq!(expand_cohorts("type:fix"), vec!["global", "type:fix"]);
        assert_eq!(expand_cohorts("global"), vec!["global"]);
        assert_eq!(expand_cohorts(""), vec!["global"]);
    }

    #[test]
    fn round_secs_clamps_negative_to_zero() {
        assert_eq!(round_secs(-5.0), 0);
        assert_eq!(round_secs(0.4), 0);
        assert_eq!(round_secs(0.6), 1);
        assert_eq!(round_secs(1234.5), 1235);
    }
}
