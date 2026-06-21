-- 20260621_019_agent_run_links
-- Wave 3 of the AI/agent flywheel: let a logged agent run link to the PR/issue it
-- produced and name the agent, so agent work is attributable and feeds the same
-- outcome signals (merged-PR cycle time) that calibrate future estimates.
-- Forward-only; all additive nullable columns (no backfill needed).

ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS pr_id      uuid REFERENCES pull_requests(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS issue_id   uuid REFERENCES issues(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS agent_name text,   -- e.g. 'claude-code', 'cursor', 'custom'
    ADD COLUMN IF NOT EXISTS branch     text;    -- branch the run worked on

CREATE INDEX IF NOT EXISTS agent_runs_pr_idx ON agent_runs (pr_id);
CREATE INDEX IF NOT EXISTS agent_runs_issue_idx ON agent_runs (issue_id);
