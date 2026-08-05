//! gitstate-calibrate — empirical-Bayes effort calibration.
//!
//! Turns a judged difficulty score ([`gitstate_core::EffortEstimate::difficulty`])
//! into a calibrated effort-in-seconds estimate that learns from this
//! machine's own merged-PR history. Ported from Go's `internal/calibration`
//! (see `docs/PORT-PLAN.md` §2, wave 2).
//!
//! Three modules, matching the Go package's own file split 1:1:
//! - [`cohort`] — pure, deterministic derivation of `cohort_key` /
//!   `size_bucket` / `change_type` from a PR + its diff metadata.
//! - [`curve`] — the math: a fixed cold-start difficulty→secs prior,
//!   empirical-Bayes shrinkage toward the global cohort, recency-weighted
//!   quantiles, and the calibrated-seconds read path.
//! - [`recompute`] — the closed loop: backfill actuals, rebuild the
//!   per-cohort curves, and the per-cohort accuracy summary.
//!
//! **Not wired to a live caller yet, same as the Go reference.** Nothing in
//! either tree currently calls `cohort::cohort_candidates`/`change_type` at
//! estimate-write time, and Go's own `RecomputeCalibration`/`CalibratedSecs`
//! have no caller either (their only intended caller, the multi-tenant HTTP
//! API, was already removed as SaaS-only). This crate exists so wave 3
//! (`context_bundle`'s `EstimateBrief`) has real calibration math to call
//! instead of a stub — see `docs/PORT-PLAN.md` §4's dependency graph.

pub mod cohort;
pub mod curve;
pub mod recompute;
