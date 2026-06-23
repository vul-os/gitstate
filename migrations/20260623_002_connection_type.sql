-- 20260623_002_connection_type
-- Support GitHub App installations + GitLab group access tokens alongside OAuth.
-- connection_type distinguishes how the token is sourced; installation_id holds the
-- GitHub App installation id (per-org) or the GitLab group path. The App PRIVATE KEY
-- is a server secret (config/env), never stored here. Forward-only.

ALTER TABLE platform_connections
    ADD COLUMN IF NOT EXISTS connection_type text NOT NULL DEFAULT 'oauth',
    ADD COLUMN IF NOT EXISTS installation_id  text;

-- token_encrypted is now optional: GitHub App connections store no long-lived token
-- (a 1-hour installation token is minted on demand from the app key + installation_id,
-- then cached back into token_encrypted/expires_at). Drop the NOT NULL if present.
ALTER TABLE platform_connections ALTER COLUMN token_encrypted DROP NOT NULL;
