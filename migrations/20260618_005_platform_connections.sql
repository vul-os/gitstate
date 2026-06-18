-- 20260618_005_platform_connections
-- Per-org GitHub/GitLab app connections: stores the encrypted access token obtained
-- via the OAuth-app authorize flow so sync no longer re-supplies a PAT each time.
-- forward-only.

CREATE TABLE platform_connections (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    platform        text NOT NULL,                 -- github | gitlab
    connected_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    external_login  text,                           -- the connected account login/username
    token_encrypted bytea,                          -- AES-256-GCM encrypted access token (internal/crypto)
    refresh_encrypted bytea,                        -- optional encrypted refresh token (gitlab)
    scopes          text,
    expires_at      timestamptz,
    base_url        text,                           -- gitlab self-hosted base, null for SaaS
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, platform)
);
CREATE INDEX ON platform_connections (org_id);

ALTER TABLE platform_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON platform_connections
    USING (org_id = current_org())
    WITH CHECK (org_id = current_org());
