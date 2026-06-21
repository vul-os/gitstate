-- 20260621_022_analytics_events
-- Instance-wide product analytics for the (server-rendered, super-admin-only)
-- cloud operator console: signups, logins, page/api activity, enriched with
-- coarse geo. PRIVACY: the raw IP is never retained — only a salted hash (so
-- repeat visitors can be counted without storing PII) plus coarse geo (country/
-- region/city) resolved from an optional local MaxMind/db-ip database.
--
-- This is a GLOBAL table (not org-scoped): it is written by the app on auth/nav
-- events and read ONLY through the BYPASSRLS admin pool by the super-admin
-- console — never exposed on the org-scoped /api surface. Forward-only.

CREATE TABLE analytics_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        text NOT NULL,              -- signup | login | login_failed | pageview | api | logout | …
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    org_id      uuid,                        -- optional; no FK so cross-org analytics stays simple
    path        text,
    method      text,
    status      int,
    ip_hash     text,                        -- sha256(ip + salt); raw IP never stored
    country     text,                        -- ISO-3166 alpha-2 (from geo), '' when unknown
    region      text,
    city        text,
    user_agent  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON analytics_events (created_at DESC);
CREATE INDEX ON analytics_events (kind, created_at DESC);
CREATE INDEX ON analytics_events (country);
