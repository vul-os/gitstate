-- 20260618_008_leave_types_balances
-- Richer leave management (inspired by dedicated leave tools): configurable leave
-- TYPES, per-user BALANCES (entitled / used / carried), and half-day support.
-- forward-only.

CREATE TABLE leave_types (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,                 -- Vacation, Sick, Personal, Parental, …
    color            text NOT NULL DEFAULT '#2DD4BF',
    default_days     numeric NOT NULL DEFAULT 0,     -- annual entitlement default
    requires_approval boolean NOT NULL DEFAULT true,
    accrues          boolean NOT NULL DEFAULT false, -- accrue monthly vs granted up front
    carryover_max    numeric NOT NULL DEFAULT 0,     -- max days carried into next year
    paid             boolean NOT NULL DEFAULT true,
    archived         boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE INDEX ON leave_types (org_id);

CREATE TABLE leave_balances (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    leave_type_id uuid NOT NULL REFERENCES leave_types(id) ON DELETE CASCADE,
    year          int  NOT NULL,
    entitled_days numeric NOT NULL DEFAULT 0,
    carried_days  numeric NOT NULL DEFAULT 0,
    used_days     numeric NOT NULL DEFAULT 0,        -- approved leave consumed
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, user_id, leave_type_id, year)
);
CREATE INDEX ON leave_balances (org_id);
CREATE INDEX ON leave_balances (user_id);

-- Link leave entries to a type and support half-days.
ALTER TABLE leave_entries
    ADD COLUMN IF NOT EXISTS leave_type_id uuid REFERENCES leave_types(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS half_day boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS portion  text NOT NULL DEFAULT 'full'; -- full | am | pm

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['leave_types','leave_balances'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', t);
    EXECUTE format($p$CREATE POLICY org_isolation ON %I
        USING (org_id = current_org()) WITH CHECK (org_id = current_org());$p$, t);
  END LOOP;
END $$;
