-- Empirical-Bayes effort calibration: turns a judged difficulty (1..=10, see
-- `EffortEstimate.difficulty`) into a calibrated seconds estimate that learns
-- from this machine's own merged-PR history.
--
-- Ported from Go's `store/calibration.go` (migration 017 there). That schema
-- was `org_id`-scoped under Postgres RLS (`db.WithOrg`) because it lived on a
-- multi-tenant server; gitstate is now single-user and single-machine, so
-- `org_id` is dropped from every row here — one tenant, nothing left to scope.
-- See docs/PORT-PLAN.md §2/§4 and decisions.md T11 (matches wave 1's
-- `agent_runs` reasoning exactly).
--
-- Additive columns on `effort`, following the ALTER TABLE precedent 0002 set
-- for `sync_ops.author`/`sig`: existing rows and the `item_id` primary key are
-- untouched, and all five are nullable because most effort rows will never be
-- calibrated (calibration is opt-in machinery a later wave wires a writer for
-- — see the crate doc on `gitstate-calibrate` for what is and is not wired
-- today).
--
-- CAVEAT (kept faithful to Go's shape, not fixed here): `effort`'s write path
-- (`Store::save_effort`) is `INSERT OR REPLACE`, which replaces the WHOLE row
-- on a re-judge and would silently reset these five columns back to NULL,
-- because a plain re-insert never mentions them. Go's `effort_estimates` does
-- not have this hazard: each estimate there is its OWN new row (a fresh
-- `id`), so re-estimating never overwrites a prior row's calibration columns.
-- Rust's `effort` table is keyed on `item_id` (one row per work item, chosen
-- in wave 0, before this wave) rather than one row per estimate-in-time, so
-- the two schemas are not equivalent here. This is a latent interaction, not
-- an active bug: nothing in this wave calls `save_effort` after populating
-- calibration columns (there is no live writer yet — see the crate doc).
-- Flagging it for whichever later wave wires a real write path, rather than
-- redesigning `effort`'s upsert semantics as a side effect of this migration.
ALTER TABLE effort ADD COLUMN predicted_secs REAL;   -- calibrated estimate at write time
ALTER TABLE effort ADD COLUMN actual_secs INTEGER;   -- observed lead time, backfilled at merge
ALTER TABLE effort ADD COLUMN cohort_key TEXT;       -- the cohort this estimate was converted under
ALTER TABLE effort ADD COLUMN size_bucket TEXT;      -- xs|s|m|l|xl
ALTER TABLE effort ADD COLUMN change_type TEXT;      -- feature|fix|refactor|chore|docs|test

-- One recency-weighted curve cell per (cohort_key, difficulty_bucket). A cell
-- is the empirical p25/median/p75/mean lead time (seconds) observed for that
-- cohort at that rounded difficulty, plus the sample count `n` the read path
-- uses to decide whether to trust the cell outright, shrink it toward a
-- coarser prior, or fall back further (see `gitstate_calibrate::curve`).
CREATE TABLE effort_calibration (
  cohort_key        TEXT NOT NULL,
  difficulty_bucket INTEGER NOT NULL,   -- round(difficulty), clamped 1..=10
  median_secs       INTEGER NOT NULL,
  p25_secs          INTEGER NOT NULL,
  p75_secs          INTEGER NOT NULL,
  mean_secs         INTEGER NOT NULL,
  n                 INTEGER NOT NULL,
  updated_at        TEXT NOT NULL,
  PRIMARY KEY (cohort_key, difficulty_bucket)
);

-- Per-cohort estimation accuracy (MAE + bias ratio) over every estimate that
-- has both a prediction and an observed actual. Read-only summary; written
-- only by `RecomputeCalibration`'s accuracy pass.
CREATE TABLE effort_accuracy (
  cohort_key  TEXT PRIMARY KEY,
  n           INTEGER NOT NULL,
  mae_secs    INTEGER NOT NULL,
  bias_ratio  REAL NOT NULL,   -- mean(predicted/actual); <1 = under-estimating
  updated_at  TEXT NOT NULL
);
