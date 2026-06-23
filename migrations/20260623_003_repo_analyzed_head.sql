-- 20260623_003_repo_analyzed_head
-- Track the HEAD sha the deep blame/SZZ analysis last ran against, so a re-sync can
-- SKIP the expensive contribution analysis when nothing changed. last_analyzed_at is
-- informational. Forward-only.

ALTER TABLE repos
    ADD COLUMN IF NOT EXISTS last_analyzed_sha text,
    ADD COLUMN IF NOT EXISTS last_analyzed_at  timestamptz;
