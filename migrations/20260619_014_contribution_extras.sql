-- 20260619_014_contribution_extras
-- Contribution over time (cached period snapshots), an advisory equity ledger
-- (suggested vs actual share informed by contribution), and peer kudos (the SPACE
-- "satisfaction" axis + a partial answer to reviewer collusion). forward-only.

CREATE TABLE contribution_snapshots (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start date NOT NULL,
    period_end   date NOT NULL,
    composite    numeric NOT NULL DEFAULT 0,
    dimensions   jsonb NOT NULL DEFAULT '{}',   -- {shipped, review, effort, quality, ownership, durability}
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, user_id, period_start, period_end)
);
CREATE INDEX ON contribution_snapshots (org_id, period_start);

CREATE TABLE equity_allocations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start  date NOT NULL,
    period_end    date NOT NULL,
    -- advisory: % of the contribution-allocated pool suggested by the model
    suggested_pct numeric NOT NULL DEFAULT 0,
    -- what was actually granted (entered by an admin) — model is advisory only
    actual_pct    numeric,
    pool_label    text NOT NULL DEFAULT 'Contribution pool',
    note          text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, user_id, period_start, period_end)
);
CREATE INDEX ON equity_allocations (org_id);

CREATE TABLE kudos (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    from_user   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dimension   text,                          -- optional: ties to a contribution axis
    message     text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON kudos (org_id, to_user);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['contribution_snapshots','equity_allocations','kudos'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', t);
    EXECUTE format($p$CREATE POLICY org_isolation ON %I
        USING (org_id = current_org()) WITH CHECK (org_id = current_org());$p$, t);
  END LOOP;
END $$;
