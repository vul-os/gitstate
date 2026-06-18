<!-- title: Architecture | order: 4 -->

# Architecture

gitstate is a single Go binary plus a React frontend, backed by Postgres with row-level security.

## Components

| Layer | Tech | Notes |
|---|---|---|
| Backend | Go 1.25, single static binary | great concurrency for repo sync + LLM fan-out; cheap to run |
| Frontend | React 19 + Vite + Tailwind (JSX) | served standalone in dev, embedded in the binary for prod |
| Database | Postgres (Neon) | **row-level security** enforces multi-tenant isolation |
| Auth | internal JWT + rotating refresh | optional Google/Microsoft OAuth (config-gated) |
| Billing (EE) | Paystack | billed in USD, charged in ZAR at capture-time FX |

## How state is derived

1. **Git engine** clones repos and reads commits, diffs, blame, and lead times.
2. **Sync** pulls issues/PRs from GitHub *and* GitLab into one unified model, and auto-progresses
   issues from linked PR state (open → in progress, merged → done).
3. **LLM** reads diffs to estimate semantic difficulty and synthesize status — never to rank people.
4. **Metrics** compute cycle time (DORA) and involvement *texture*; reporting answers natural-language
   questions over a SELECT-only, RLS-scoped query path.

## Tenancy & security

Every org-scoped table has a row-level-security policy. Requests run inside a transaction that sets
`app.current_org`, so isolation is enforced by the database — not just app code — and proven by an
automated cross-org test. Super-admin access is audited. See [Security](/docs/security).

## Open core + Enterprise

The core is AGPL-3.0. The `ee/` directory (Paystack billing, cross-org admin) is commercial and
compiled only with `-tags ee`. Default builds are fully open and self-hostable.
