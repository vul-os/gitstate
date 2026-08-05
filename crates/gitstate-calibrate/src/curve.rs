//! The math: a fixed cold-start difficulty→seconds prior, empirical-Bayes
//! shrinkage toward the global cohort, recency-weighted quantiles, and the
//! calibrated-seconds read path.
//!
//! Ported 1:1 from Go's `internal/calibration/curve.go`. Every constant,
//! formula, and tie-break here matches the Go original exactly — see the
//! module doc on `gitstate_calibrate` for why fidelity, not just "compiles",
//! is this domain's whole risk.

use std::collections::HashMap;

use gitstate_core::{CalibrationCell, Result, Store};

use crate::cohort::GLOBAL_COHORT;

// ── tunables (Go: package vars, mutable only in tests; here: plain consts —
// nothing in this codebase mutates them at runtime either) ─────────────────

/// The sample floor for a cohort to be used directly. Below it we either
/// shrink toward the global prior or walk to a coarser cohort.
pub const MIN_COHORT_N: i64 = 5;

/// The empirical-Bayes pseudo-count: the calibrated median is
/// `(n*cohortMedian + k*globalMedian)/(n+k)`. Larger k => trust the prior
/// more until the cohort accumulates evidence.
pub const SHRINKAGE_K: f64 = 8.0;

/// The age (in seconds) at which a merged PR's weight halves in the
/// recency-weighted quantiles. Recent merges dominate the curve. Go's
/// `RecencyHalfLife` is `90 * 24 * time.Hour`; expressed here in seconds
/// (the unit [`recency_weight`] and [`Sample::merged_at_secs`] both use) so
/// this crate does not need a wall-clock duration type of its own.
pub const RECENCY_HALF_LIFE_SECS: f64 = 90.0 * 24.0 * 3600.0;

// ── cold-start prior ───────────────────────────────────────────────────────

/// The fixed difficulty→seconds curve used when there is NO calibration data
/// at all (true cold start) and as the global prior's floor. Intentionally
/// smooth and monotonic: difficulty 1 ≈ 30min, difficulty 5 ≈ 1 day of
/// focused work, difficulty 10 ≈ ~2 weeks. Exponential because semantic
/// difficulty compounds (a 10 is not 10x a 1).
pub fn default_secs_for_difficulty(difficulty: f64) -> f64 {
    let d = clamp_difficulty(difficulty);
    // 1800s (30m) at d=1, growing ~3.5x per +2 difficulty -> ~9.7e5s (~11d) at d=10.
    const BASE: f64 = 1800.0;
    BASE * 3.5_f64.powf((d - 1.0) / 2.0)
}

fn clamp_difficulty(d: f64) -> f64 {
    d.clamp(1.0, 10.0)
}

/// Rounds a 1-10 difficulty to the integer bucket used as the
/// `effort_calibration.difficulty_bucket` key.
pub fn difficulty_bucket(difficulty: f64) -> i64 {
    clamp_difficulty(difficulty).round() as i64
}

// ── recency-weighted quantiles ──────────────────────────────────────────────

/// One observed (merged) outcome: the actual lead time in seconds and the
/// merge timestamp (as seconds since the Unix epoch) used to age-weight it.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Sample {
    pub actual_secs: i64,
    pub merged_at_epoch_secs: f64,
}

/// The exponential-decay weight of a sample given its age relative to `now`,
/// with the configured half-life. `age <= 0` => weight 1.
pub fn recency_weight(now_epoch_secs: f64, merged_at_epoch_secs: f64, half_life_secs: f64) -> f64 {
    let age = now_epoch_secs - merged_at_epoch_secs;
    if age <= 0.0 || half_life_secs <= 0.0 {
        return 1.0;
    }
    0.5_f64.powf(age / half_life_secs)
}

/// The outcome of [`weighted_quantiles`].
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Quantiles {
    pub p25: f64,
    pub median: f64,
    pub p75: f64,
    pub mean: f64,
    pub n: usize,
}

/// Computes the recency-weighted p25/median/p75/mean over `samples`. Returns
/// `None` when there are no samples (or every sample's weight is zero/non-
/// positive). The weighted quantile is the classic cumulative-weight
/// interpolation: the value at which the running weight fraction crosses `q`.
pub fn weighted_quantiles(
    samples: &[Sample],
    now_epoch_secs: f64,
    half_life_secs: f64,
) -> Option<Quantiles> {
    if samples.is_empty() {
        return None;
    }

    struct Wv {
        v: f64,
        w: f64,
    }
    let mut pts: Vec<Wv> = Vec::with_capacity(samples.len());
    let mut total_w = 0.0_f64;
    let mut weighted_sum = 0.0_f64;
    for s in samples {
        let w = recency_weight(now_epoch_secs, s.merged_at_epoch_secs, half_life_secs);
        if w <= 0.0 {
            continue;
        }
        let v = s.actual_secs as f64;
        pts.push(Wv { v, w });
        total_w += w;
        weighted_sum += v * w;
    }
    if pts.is_empty() || total_w <= 0.0 {
        return None;
    }

    pts.sort_by(|a, b| a.v.partial_cmp(&b.v).unwrap());

    let q = |target: f64| -> f64 {
        // Cumulative weight at the MIDPOINT of each point's weight band,
        // then linearly interpolate between bracketing points (Type-7-like).
        let mut cum = 0.0_f64;
        let mut prev_v = pts[0].v;
        let mut prev_c = 0.0_f64;
        for (i, p) in pts.iter().enumerate() {
            let mid_c = (cum + p.w / 2.0) / total_w;
            if mid_c >= target {
                if i == 0 {
                    return p.v;
                }
                let span = mid_c - prev_c;
                if span <= 0.0 {
                    return p.v;
                }
                let frac = (target - prev_c) / span;
                return prev_v + frac * (p.v - prev_v);
            }
            cum += p.w;
            prev_v = p.v;
            prev_c = mid_c;
        }
        pts[pts.len() - 1].v
    };

    let mean = weighted_sum / total_w;
    Some(Quantiles {
        p25: q(0.25),
        median: q(0.50),
        p75: q(0.75),
        mean,
        n: pts.len(),
    })
}

/// Applies empirical-Bayes shrinkage of a cohort estimate toward a prior
/// given the cohort's sample size `n` and pseudo-count `k`:
///
/// ```text
/// est = (n*cohort + k*prior) / (n + k)
/// ```
///
/// As `n -> infinity` the cohort dominates; at `n = 0` it returns the prior.
/// Pure and testable.
pub fn shrink_to_prior(cohort: f64, prior: f64, n: i64, k: f64) -> f64 {
    let fn_ = if n < 0 { 0.0 } else { n as f64 };
    if fn_ + k == 0.0 {
        return prior;
    }
    (fn_ * cohort + k * prior) / (fn_ + k)
}

// ── calibrated_secs (read path) ─────────────────────────────────────────────

/// The outcome of a calibration lookup.
#[derive(Debug, Clone, PartialEq)]
pub struct CalibratedResult {
    pub predicted_secs: f64,
    /// The cohort actually used (after walking richest-first). Is
    /// [`GLOBAL_COHORT`] on the cold-start default path.
    pub cohort_key: String,
    /// True when empirical-Bayes shrinkage toward the global prior was
    /// applied (the chosen cohort had `0 < n < MIN_COHORT_N`).
    pub shrunk: bool,
    /// True when no data existed at all and the fixed prior was used.
    pub cold_start: bool,
}

/// Converts a model difficulty into calibrated seconds. Walks
/// `cohort_candidates` RICHEST-FIRST and selects the first cohort with
/// `n >= MIN_COHORT_N` at the rounded difficulty bucket. When the best
/// available cohort is sparse it applies empirical-Bayes shrinkage toward
/// the global-cohort median. When there is no data at all it falls back to
/// the fixed [`default_secs_for_difficulty`] prior.
///
/// Must be safe to call cold (no rows) — it never errors on "no data", only
/// on real storage failures.
pub fn calibrated_secs(
    store: &dyn Store,
    difficulty: f64,
    cohort_candidates: &[String],
) -> Result<CalibratedResult> {
    let bucket = difficulty_bucket(difficulty);

    let mut keys: Vec<String> = cohort_candidates.to_vec();
    keys.push(GLOBAL_COHORT.to_string());
    let rows: HashMap<String, CalibrationCell> = store.get_calibration_cells(&keys, bucket)?;

    let (global_median, has_global) = match rows.get(GLOBAL_COHORT) {
        Some(g) if g.n > 0 && g.median_secs > 0 => (g.median_secs as f64, true),
        _ => (0.0, false),
    };

    let prior = default_secs_for_difficulty(difficulty);

    // Walk richest-first; the first cohort with enough samples wins outright.
    for key in cohort_candidates {
        let Some(cell) = rows.get(key) else { continue };
        if cell.n <= 0 || cell.median_secs <= 0 {
            continue;
        }
        let cohort_median = cell.median_secs as f64;

        if cell.n >= MIN_COHORT_N {
            return Ok(CalibratedResult {
                predicted_secs: cohort_median,
                cohort_key: key.clone(),
                shrunk: false,
                cold_start: false,
            });
        }

        // Sparse cohort: empirical-Bayes shrink toward the prior. Use the
        // global median as the prior when available, else the fixed curve.
        let prior_val = if has_global && key != GLOBAL_COHORT {
            global_median
        } else {
            prior
        };
        let est = shrink_to_prior(cohort_median, prior_val, cell.n, SHRINKAGE_K);
        return Ok(CalibratedResult {
            predicted_secs: est,
            cohort_key: key.clone(),
            shrunk: true,
            cold_start: false,
        });
    }

    // No usable cohort row matched the candidates; fall back to global if present.
    if has_global {
        return Ok(CalibratedResult {
            predicted_secs: global_median,
            cohort_key: GLOBAL_COHORT.to_string(),
            shrunk: false,
            cold_start: false,
        });
    }

    // True cold start: fixed prior.
    Ok(CalibratedResult {
        predicted_secs: prior,
        cohort_key: GLOBAL_COHORT.to_string(),
        shrunk: false,
        cold_start: true,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_secs_for_difficulty_monotonic_and_clamped() {
        // Ported from curve_test.go's TestDefaultSecsForDifficultyMonotonic.
        let mut prev = 0.0;
        let mut d = 1.0;
        while d <= 10.0 {
            let got = default_secs_for_difficulty(d);
            assert!(got > prev, "not increasing at d={d}: {got} <= {prev}");
            prev = got;
            d += 0.5;
        }
        assert_eq!(
            default_secs_for_difficulty(0.0),
            default_secs_for_difficulty(1.0),
            "difficulty<1 should clamp to 1"
        );
        assert_eq!(
            default_secs_for_difficulty(99.0),
            default_secs_for_difficulty(10.0),
            "difficulty>10 should clamp to 10"
        );
        let d1 = default_secs_for_difficulty(1.0);
        assert!((d1 - 1800.0).abs() <= 1.0, "d=1 want ~1800s, got {d1}");
        let d10 = default_secs_for_difficulty(10.0);
        assert!(
            d10 >= 5.0 * 24.0 * 3600.0,
            "d=10 want multi-day, got {d10}s"
        );
    }

    #[test]
    fn difficulty_bucket_matches_go_fixtures() {
        // Ported from curve_test.go's TestDifficultyBucket.
        assert_eq!(difficulty_bucket(1.0), 1);
        assert_eq!(difficulty_bucket(1.4), 1);
        assert_eq!(difficulty_bucket(1.5), 2);
        assert_eq!(difficulty_bucket(4.6), 5);
        assert_eq!(difficulty_bucket(9.9), 10);
        assert_eq!(difficulty_bucket(0.2), 1);
        assert_eq!(difficulty_bucket(11.0), 10);
    }

    #[test]
    fn shrink_to_prior_matches_go_fixtures() {
        // Ported from curve_test.go's TestShrinkToPrior.
        const COHORT: f64 = 100.0;
        const PRIOR: f64 = 1000.0;
        const K: f64 = 8.0;

        assert_eq!(
            shrink_to_prior(COHORT, PRIOR, 0, K),
            PRIOR,
            "n=0 => pure prior"
        );
        assert!(
            (shrink_to_prior(COHORT, PRIOR, 8, K) - 550.0).abs() < 1e-9,
            "n=k => exact midpoint"
        );
        assert!(
            (shrink_to_prior(COHORT, PRIOR, 1000, K) - COHORT).abs() < 10.0,
            "large n => approaches cohort"
        );

        let mut prev = PRIOR;
        for n in 0..=50 {
            let got = shrink_to_prior(COHORT, PRIOR, n, K);
            assert!(
                got <= prev + 1e-9,
                "not monotonically decreasing toward cohort at n={n}: {got} > {prev}"
            );
            prev = got;
        }
    }

    #[test]
    fn recency_weight_matches_go_fixtures() {
        // Ported from curve_test.go's TestRecencyWeight.
        let now = 1_780_000_000.0_f64; // arbitrary fixed instant
        let hl = 90.0 * 24.0 * 3600.0;

        assert_eq!(recency_weight(now, now, hl), 1.0, "age 0 => weight 1");
        assert!(
            (recency_weight(now, now - hl, hl) - 0.5).abs() < 1e-9,
            "one half-life => ~0.5"
        );
        assert!(
            (recency_weight(now, now - 2.0 * hl, hl) - 0.25).abs() < 1e-9,
            "two half-lives => ~0.25"
        );
        assert_eq!(
            recency_weight(now, now + 3600.0, hl),
            1.0,
            "future merge clamps to 1"
        );
    }

    #[test]
    fn weighted_quantiles_matches_go_fixtures() {
        // Ported from curve_test.go's TestWeightedQuantiles.
        let now = 1_780_000_000.0_f64;
        let hl = 90.0 * 24.0 * 3600.0;

        assert!(weighted_quantiles(&[], now, hl).is_none(), "empty => None");

        let eq = [100, 200, 300, 400, 500].map(|s| Sample {
            actual_secs: s,
            merged_at_epoch_secs: now,
        });
        let q = weighted_quantiles(&eq, now, hl).expect("equal-weight quantiles");
        assert_eq!(q.n, 5);
        assert!(
            (q.median - 300.0).abs() < 1e-6,
            "equal median want 300, got {}",
            q.median
        );
        assert!(
            (q.mean - 300.0).abs() < 1e-6,
            "equal mean want 300, got {}",
            q.mean
        );
        assert!(
            q.p25 >= 100.0 && q.p25 < q.median && q.p75 > q.median && q.p75 <= 500.0,
            "quantile ordering off: p25={} med={} p75={}",
            q.p25,
            q.median,
            q.p75
        );

        // Recency pull: an old huge value should be down-weighted so the
        // weighted median stays near the recent small cluster.
        let skew = [
            Sample {
                actual_secs: 100,
                merged_at_epoch_secs: now,
            },
            Sample {
                actual_secs: 110,
                merged_at_epoch_secs: now,
            },
            Sample {
                actual_secs: 120,
                merged_at_epoch_secs: now,
            },
            Sample {
                actual_secs: 100_000,
                merged_at_epoch_secs: now - 10.0 * hl,
            },
        ];
        let qs = weighted_quantiles(&skew, now, hl).expect("skewed quantiles");
        assert!(
            qs.median <= 1000.0,
            "recency-weighted median should ignore ancient outlier, got {}",
            qs.median
        );
        assert!(
            qs.mean <= 5000.0,
            "recency-weighted mean should be dominated by recent samples, got {}",
            qs.mean
        );
    }
}
