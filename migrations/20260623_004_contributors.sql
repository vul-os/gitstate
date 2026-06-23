-- 20260623_004_contributors
-- Contributor identity system: cluster the many git identities (emails + logins)
-- that are actually ONE person into a canonical contributor, optionally linked to a
-- gitstate user/member, with exclude + invite support. Contribution/analytics
-- aggregate by contributor_id (via contributor_identities) instead of raw git ident.
-- Org-scoped, FORCE RLS. Forward-only.

CREATE TABLE contributors (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    display_name  text NOT NULL DEFAULT '',
    primary_email text,                                        -- canonical/contact email
    user_id       uuid REFERENCES users(id) ON DELETE SET NULL, -- linked system member (nullable)
    excluded      boolean NOT NULL DEFAULT false,              -- drop from contribution/analytics
    is_bot        boolean NOT NULL DEFAULT false,
    invited_at    timestamptz,                                 -- an invite was sent to link a user
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON contributors (org_id);
CREATE INDEX ON contributors (org_id, user_id);

-- Each git identity (an email OR a login) belongs to exactly ONE contributor.
CREATE TABLE contributor_identities (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contributor_id uuid NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
    kind           text NOT NULL,                  -- 'email' | 'login'
    value          text NOT NULL,                  -- always stored lowercased
    name_seen      text,                            -- the author display name last seen for this identity
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, kind, value)
);
CREATE INDEX ON contributor_identities (org_id, contributor_id);
CREATE INDEX ON contributor_identities (org_id, value);

ALTER TABLE contributors ENABLE ROW LEVEL SECURITY;
ALTER TABLE contributors FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON contributors
    USING (org_id = current_org()) WITH CHECK (org_id = current_org());

ALTER TABLE contributor_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE contributor_identities FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON contributor_identities
    USING (org_id = current_org()) WITH CHECK (org_id = current_org());
