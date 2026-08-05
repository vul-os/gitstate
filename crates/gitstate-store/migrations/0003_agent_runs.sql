-- The agent-run write path: an AI agent records what it did (goal, the shape
-- of the diff, whether its own tests passed, and — once a human has looked —
-- their verdict), closing the loop into attribution + estimation the same
-- way a human's commit already does.
--
-- Ported from Go's `store/agent_runs.go`. That table was `org_id`-scoped and
-- read/written under Postgres RLS (`db.WithOrg`) because it lived on a
-- multi-tenant server; gitstate is now single-user and single-machine, so
-- there is exactly one tenant — this node — and `org_id` has nothing left to
-- distinguish. It is dropped, not carried forward as a column that would
-- always hold the same value. See docs/PORT-PLAN.md §4 and decisions.md T11.
--
-- `pr_id`/`issue_id` both reference `work_items(id)`: gitstate models PRs and
-- issues as one WorkItem table (`kind = 'pr' | 'issue'`), not the two
-- Postgres tables (`pull_requests`, `issues`) the Go version joined against —
-- those were never ported (superseded by WorkItem when `internal/contribution`
-- moved). `ON DELETE SET NULL` rather than CASCADE: deleting the work item a
-- run once pointed at should not erase the historical fact that the run
-- happened, only the now-dangling link.
--
-- `supervisor_id` is free TEXT with no FK. The Go column referenced `users.id`
-- (an org-member account) — a concept this single-user app has no table for.
-- Keeping the column but dropping the constraint preserves the write shape
-- (a caller may still say who supervised a run) without inventing a users
-- table whose only row would always be "the one operator".
CREATE TABLE agent_runs (
  id             TEXT PRIMARY KEY,
  repo_id        TEXT REFERENCES repos(id) ON DELETE SET NULL,
  pr_id          TEXT REFERENCES work_items(id) ON DELETE SET NULL,
  issue_id       TEXT REFERENCES work_items(id) ON DELETE SET NULL,
  supervisor_id  TEXT,
  goal           TEXT NOT NULL,
  agent_name     TEXT,
  branch         TEXT,
  diff_summary   TEXT NOT NULL DEFAULT '{}',   -- JSON AgentDiffSummary
  tests_passed   INTEGER,                       -- NULL=unknown, else 0/1
  human_action   TEXT,                          -- NULL | accepted | edited | reverted
  iterations     INTEGER,
  cost_usd       REAL,
  created_at     TEXT NOT NULL
);

-- The list path is always newest-first; this index makes that scan (and its
-- LIMIT) an index-order read instead of a full-table sort.
CREATE INDEX idx_agent_runs_created ON agent_runs(created_at DESC, id DESC);
-- One index per optional filter column `list_agent_runs` narrows on.
CREATE INDEX idx_agent_runs_repo ON agent_runs(repo_id);
CREATE INDEX idx_agent_runs_pr ON agent_runs(pr_id);
CREATE INDEX idx_agent_runs_issue ON agent_runs(issue_id);
CREATE INDEX idx_agent_runs_agent ON agent_runs(agent_name);
