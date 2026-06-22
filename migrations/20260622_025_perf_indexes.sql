-- 20260622_025_perf_indexes
-- Performance: indexes on hot org-scoped lookups that were doing Seq Scans on
-- seeded data (verified via EXPLAIN ANALYZE: Seq Scan → Index/Bitmap Scan).
-- Forward-only; all IF NOT EXISTS.

-- cycle_times per-PR (every UpsertCycleTime delete + the context-bundle lead-time lateral)
CREATE INDEX IF NOT EXISTS cycle_times_org_id_pr_id_computed_at_idx
    ON cycle_times (org_id, pr_id, computed_at DESC);

-- effort_estimates per-PR / per-issue (GetEstimateForPR/Issue, context bundle, calibration)
CREATE INDEX IF NOT EXISTS effort_estimates_org_id_pr_id_created_at_idx
    ON effort_estimates (org_id, pr_id, created_at DESC);
CREATE INDEX IF NOT EXISTS effort_estimates_org_id_issue_id_created_at_idx
    ON effort_estimates (org_id, issue_id, created_at DESC);

-- pull_requests list by repo (hottest PR read: metrics, PR lists, sync)
CREATE INDEX IF NOT EXISTS pull_requests_repo_id_created_at_idx
    ON pull_requests (repo_id, created_at DESC);

-- issues list by repo (sync goroutine)
CREATE INDEX IF NOT EXISTS issues_org_id_repo_id_idx
    ON issues (org_id, repo_id);
