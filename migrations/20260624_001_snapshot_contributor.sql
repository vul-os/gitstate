-- 20260624_001_snapshot_contributor
-- Re-key contribution_snapshots (the trend cache) by the canonical CONTRIBUTOR
-- (the person) instead of only by user_id. Most contributors are not linked to a
-- gitstate user, so a user_id-only snapshot table can never hold a trend for a
-- grouped, unlinked person — every grouped person's sparkline reads empty.
--
-- Forward-only. The table already has FORCE ROW LEVEL SECURITY + an org_isolation
-- policy (baseline 20260618_001); altering columns leaves those intact.
--
-- Changes:
--   - add contributor_id uuid (FK contributors, ON DELETE CASCADE), nullable.
--   - make user_id nullable (a per-contributor snapshot may have no linked user).
--   - drop the old UNIQUE(org_id,user_id,period_start,period_end) and add a
--     UNIQUE on (org_id,contributor_id,period_start,period_end) so the per-
--     contributor upsert is idempotent. The new partial-unique uses COALESCE so
--     rows always have a non-null key (contributor_id when present, else user_id).

ALTER TABLE contribution_snapshots
    ADD COLUMN contributor_id uuid REFERENCES contributors(id) ON DELETE CASCADE;

ALTER TABLE contribution_snapshots
    ALTER COLUMN user_id DROP NOT NULL;

-- Drop the old user-keyed uniqueness (named automatically by the baseline). The
-- constraint name is the Postgres default for a table-level UNIQUE(...): it is
-- <table>_<cols>_key. Drop it defensively by discovering its name.
DO $$
DECLARE cname text;
BEGIN
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'contribution_snapshots'::regclass
      AND contype = 'u'
      AND pg_get_constraintdef(oid) ILIKE '%(org_id, user_id, period_start, period_end)%';
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE contribution_snapshots DROP CONSTRAINT %I', cname);
    END IF;
END $$;

-- New idempotency key: one snapshot per (org, person, period). The person key is
-- contributor_id when set, else the (legacy) user_id — COALESCE'd into one uuid so
-- a unique index can enforce it without nulls colliding.
CREATE UNIQUE INDEX contribution_snapshots_person_period_key
    ON contribution_snapshots (org_id, COALESCE(contributor_id, user_id), period_start, period_end);

CREATE INDEX ON contribution_snapshots (org_id, contributor_id);
