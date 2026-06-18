-- 20260618_001_init
-- Base schema: identity, multi-tenant orgs (RLS), work/git, metrics, billing, audit.
-- forward-only; a rollback is a new migration.

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive email

-- ── RLS helper ───────────────────────────────────────────────────────────
-- Request middleware runs each tx with: SET LOCAL app.current_org = '<uuid>'.
-- Policies compare against current_setting('app.current_org', true) so a missing
-- value yields NULL (no rows) rather than an error.
CREATE OR REPLACE FUNCTION current_org() RETURNS uuid
  LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.current_org', true), '')::uuid $$;

-- ── Identity ─────────────────────────────────────────────────────────────
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         citext UNIQUE NOT NULL,
    name          text,
    avatar_url    text,
    password_hash text,                       -- null when OAuth-only
    is_super_admin boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_accounts (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      text NOT NULL,              -- google | microsoft | github | gitlab
    provider_uid  text NOT NULL,
    email         citext,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_uid)
);

-- Rotating refresh tokens with reuse-detection via family_id.
CREATE TABLE refresh_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id     uuid NOT NULL,
    token_hash    text NOT NULL UNIQUE,
    replaced_by   uuid REFERENCES refresh_tokens(id),
    revoked_at    timestamptz,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON refresh_tokens (user_id);
CREATE INDEX ON refresh_tokens (family_id);

-- ── Tenancy ──────────────────────────────────────────────────────────────
CREATE TABLE organizations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          citext UNIQUE NOT NULL,
    name          text NOT NULL,
    plan_key      text NOT NULL DEFAULT 'free',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- role: owner | admin | member (=builder, billable) | stakeholder (FREE per wedge P6)
CREATE TABLE org_members (
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          text NOT NULL DEFAULT 'member',
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX ON org_members (user_id);

CREATE TABLE org_invites (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         citext NOT NULL,
    role          text NOT NULL DEFAULT 'member',
    token_hash    text NOT NULL UNIQUE,
    expires_at    timestamptz NOT NULL,
    accepted_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- ── Repos & work ─────────────────────────────────────────────────────────
CREATE TABLE repos (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    platform      text NOT NULL,              -- github | gitlab
    external_id   text NOT NULL,
    full_name     text NOT NULL,              -- owner/name
    default_branch text,
    clone_url     text,
    last_synced_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, platform, external_id)
);
CREATE INDEX ON repos (org_id);

CREATE TABLE projects (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name          text NOT NULL,
    key           text,                       -- short code
    archived      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON projects (org_id);

-- Two truth-modes (decisions P1): source = 'git' (derived) | 'native' (manual).
CREATE TABLE issues (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id    uuid REFERENCES projects(id) ON DELETE SET NULL,
    repo_id       uuid REFERENCES repos(id) ON DELETE SET NULL,
    source        text NOT NULL DEFAULT 'native',  -- git | native
    platform      text,                            -- github | gitlab (when source=git)
    external_id   text,
    number        int,
    title         text NOT NULL,
    body          text,
    state         text NOT NULL DEFAULT 'open',     -- open | in_progress | done | closed
    derived_state text,                             -- computed from linked git activity
    assignee_id   uuid REFERENCES users(id),
    labels        text[] NOT NULL DEFAULT '{}',
    custom_fields jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, platform, external_id)
);
CREATE INDEX ON issues (org_id);
CREATE INDEX ON issues (project_id);

CREATE TABLE pull_requests (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repo_id       uuid NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    platform      text NOT NULL,
    external_id   text NOT NULL,
    number        int,
    title         text,
    author_login  text,
    state         text,                              -- open | merged | closed
    additions     int,
    deletions     int,
    changed_files int,
    first_commit_at timestamptz,
    merged_at     timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, repo_id, external_id)
);
CREATE INDEX ON pull_requests (org_id);

CREATE TABLE commits (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repo_id       uuid NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    sha           text NOT NULL,
    author_login  text,
    author_email  citext,
    is_agent      boolean NOT NULL DEFAULT false,    -- agent vs human authored (decisions P5)
    message       text,
    additions     int,
    deletions     int,
    committed_at  timestamptz,
    UNIQUE (org_id, repo_id, sha)
);
CREATE INDEX ON commits (org_id);
CREATE INDEX ON commits (repo_id, committed_at);

-- Markdown plan-in-repo (roadmap.md / tasks/*.md) projected as work.
CREATE TABLE task_files (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repo_id       uuid NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    path          text NOT NULL,
    parsed        jsonb NOT NULL DEFAULT '{}',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, repo_id, path)
);

-- ── Derived metrics ──────────────────────────────────────────────────────
CREATE TABLE effort_estimates (         -- LLM diff-difficulty (decisions P3)
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pr_id         uuid REFERENCES pull_requests(id) ON DELETE CASCADE,
    issue_id      uuid REFERENCES issues(id) ON DELETE CASCADE,
    difficulty    numeric,                          -- model-judged
    rationale     text,
    evidence      jsonb NOT NULL DEFAULT '{}',      -- links to git evidence
    model         text,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON effort_estimates (org_id);

CREATE TABLE cycle_times (              -- DORA-style, observed from git
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pr_id         uuid REFERENCES pull_requests(id) ON DELETE CASCADE,
    lead_time_secs bigint,                          -- first commit → merge
    review_secs   bigint,
    computed_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON cycle_times (org_id);

-- Involvement as TEXTURE (decisions P2) — never a single score.
CREATE TABLE involvement (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id    uuid REFERENCES projects(id) ON DELETE CASCADE,
    user_id       uuid REFERENCES users(id) ON DELETE SET NULL,
    period_start  date NOT NULL,
    features_shipped int NOT NULL DEFAULT 0,
    reviews_done  int NOT NULL DEFAULT 0,          -- counts the invisible work
    areas_owned   int NOT NULL DEFAULT 0,
    active        boolean NOT NULL DEFAULT true,
    dimensions    jsonb NOT NULL DEFAULT '{}',     -- extensible texture
    UNIQUE (org_id, project_id, user_id, period_start)
);
CREATE INDEX ON involvement (org_id);

-- Agent-native unit (decisions P5).
CREATE TABLE agent_runs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repo_id       uuid REFERENCES repos(id) ON DELETE SET NULL,
    goal          text,
    diff_summary  jsonb NOT NULL DEFAULT '{}',
    tests_passed  boolean,
    human_action  text,                            -- accepted | edited | reverted
    iterations    int,
    cost_usd      numeric,
    supervisor_id uuid REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON agent_runs (org_id);

-- ── Billing (EE) ─────────────────────────────────────────────────────────
CREATE TABLE plans (
    key           text PRIMARY KEY,
    name          text NOT NULL,
    usd_cents     int NOT NULL DEFAULT 0,           -- price defined in USD
    builders      int NOT NULL DEFAULT 0,
    max_conns     int NOT NULL DEFAULT 0,           -- CEILING not reservation
    features      jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE subscriptions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    plan_key      text NOT NULL REFERENCES plans(key),
    status        text NOT NULL DEFAULT 'active',   -- active | past_due | canceled
    current_period_end timestamptz,
    paystack_sub_code text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id)
);

CREATE TABLE usage_events (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind          text NOT NULL,                    -- builder_seat | llm_tokens | sync | ...
    quantity      numeric NOT NULL DEFAULT 0,
    cost_usd      numeric NOT NULL DEFAULT 0,
    occurred_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON usage_events (org_id, occurred_at);

-- USD↔ZAR rates captured at charge time (decisions A8).
CREATE TABLE exchange_rates (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    base          text NOT NULL,                    -- USD
    quote         text NOT NULL,                    -- ZAR
    rate          numeric NOT NULL,
    provider      text,
    fetched_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON exchange_rates (base, quote, fetched_at DESC);

CREATE TABLE invoices (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status        text NOT NULL DEFAULT 'draft',    -- draft | open | paid | void
    usd_cents     int NOT NULL DEFAULT 0,           -- billed in USD
    zar_cents     int,                              -- charged in ZAR
    fx_rate       numeric,                          -- rate used at capture
    fx_rate_id    uuid REFERENCES exchange_rates(id),
    period_start  date,
    period_end    date,
    paystack_ref  text,
    issued_at     timestamptz,
    paid_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON invoices (org_id);

CREATE TABLE invoice_lines (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id    uuid NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    description   text NOT NULL,
    usd_cents     int NOT NULL DEFAULT 0,
    evidence      jsonb NOT NULL DEFAULT '{}',      -- git-backed; gaps flagged (decisions P4)
    is_estimated  boolean NOT NULL DEFAULT false
);

CREATE TABLE payments (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    invoice_id    uuid REFERENCES invoices(id) ON DELETE SET NULL,
    zar_cents     int NOT NULL,
    status        text NOT NULL,                    -- success | failed | pending
    paystack_ref  text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE paystack_events (          -- webhook idempotency (decisions S4)
    id            text PRIMARY KEY,                 -- paystack event id
    type          text NOT NULL,
    payload       jsonb NOT NULL,
    processed_at  timestamptz NOT NULL DEFAULT now()
);

-- ── Platform ─────────────────────────────────────────────────────────────
CREATE TABLE audit_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id      uuid REFERENCES users(id),
    org_id        uuid REFERENCES organizations(id),
    action        text NOT NULL,
    target        text,
    meta          jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON audit_log (org_id, created_at);

CREATE TABLE feature_flags (
    key           text PRIMARY KEY,
    enabled       boolean NOT NULL DEFAULT false,
    meta          jsonb NOT NULL DEFAULT '{}'
);

-- ── Row-Level Security (decisions A2/S1) ─────────────────────────────────
-- Enable RLS + an org-scoping policy on every org-scoped table.
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'repos','projects','issues','pull_requests','commits','task_files',
    'effort_estimates','cycle_times','involvement','agent_runs',
    'subscriptions','usage_events','invoices','payments'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', t);
    EXECUTE format($p$CREATE POLICY org_isolation ON %I
        USING (org_id = current_org())
        WITH CHECK (org_id = current_org());$p$, t);
  END LOOP;
END $$;

-- Seed plans (mirror config.example.yaml ladder).
INSERT INTO plans (key, name, usd_cents, builders, max_conns) VALUES
  ('free',  'Free',        0,     2,   10),
  ('hobby', 'Hobby',       900,   5,   25),
  ('pro',   'Pro',         3900,  15,  75),
  ('team',  'Team',        19900, 50,  200),
  ('scale', 'Scale',       24900, 100, 400),
  ('ent',   'Enterprise',  0,     0,   0)
ON CONFLICT (key) DO NOTHING;
