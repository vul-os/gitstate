-- 20260618_007_calendar_connections
-- Per-user Google / Microsoft calendar connections for two-way leave/availability sync:
--   push approved leave → calendar events; pull OOO/busy → availability.
-- forward-only.

CREATE TABLE calendar_connections (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          text NOT NULL,              -- google | microsoft
    external_email    text,                        -- the connected calendar account
    calendar_id       text,                        -- target calendar (default 'primary')
    token_encrypted   bytea,                       -- AES-256-GCM access token (internal/crypto)
    refresh_encrypted bytea,                       -- AES-256-GCM refresh token
    scopes            text,
    expires_at        timestamptz,
    push_leave        boolean NOT NULL DEFAULT true,  -- push approved leave to this calendar
    pull_busy         boolean NOT NULL DEFAULT true,  -- pull OOO/busy into availability
    last_synced_at    timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, user_id, provider)
);
CREATE INDEX ON calendar_connections (org_id);
CREATE INDEX ON calendar_connections (user_id);

-- Link a leave entry to the calendar event we created, so updates/cancellations sync.
ALTER TABLE leave_entries
    ADD COLUMN IF NOT EXISTS calendar_event_id text,
    ADD COLUMN IF NOT EXISTS calendar_provider text;

ALTER TABLE calendar_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE calendar_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON calendar_connections
    USING (org_id = current_org())
    WITH CHECK (org_id = current_org());
