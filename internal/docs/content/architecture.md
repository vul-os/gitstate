<!-- title: Architecture | order: 40 | category: Reference | summary: The components, package map, request lifecycle, and open-core split. -->

# Architecture

gitstate is a single Go binary plus a React frontend, backed by Postgres with row-level security.

## Components

| Layer | Tech | Notes |
|---|---|---|
| Backend | Go 1.25, single static binary | great concurrency for repo sync + LLM fan-out; cheap to run |
| Frontend | React 19 + Vite + Tailwind (JSX, no TSX) | served standalone in dev, embedded in the binary for prod |
| Admin | Server-rendered HTML (`html/template` + htmx + SSE) | the super-admin "super pages"; not part of the SPA |
| Database | Postgres (Neon) | **row-level security** enforces multi-tenant isolation |
| Auth | internal JWT + rotating refresh | optional Google/Microsoft OAuth (config-gated) |
| Billing (EE) | Paystack | billed in USD, charged in ZAR at capture-time FX |
| Deploy | fly.io primary; Docker, compose, systemd, bare binary | no lock-in |

## Package map (`internal/`)

| Package | Responsibility |
|---|---|
| `config` | `config.yaml` + env overlay; OAuth/feature gating |
| `db` | pgx pool, `WithOrg(ctx, orgID, fn)` RLS session helper |
| `store` | hand-written SQL data access (no heavy ORM) |
| `auth` | JWT issue/verify, rotating refresh, argon2id passwords |
| `oauth` | Google + Microsoft providers (config-gated) |
| `git` | clone/fetch, walk commits, diff, blame, lead-time, agent detection |
| `sync` | GitHub + GitLab issue/PR sync; auto-progress |
| `llm` | diff-difficulty sizing + status synthesis (Anthropic default) |
| `metrics` | cycle time (DORA) + involvement texture |
| `report` | dashboards, burndown, NL→report (SELECT-only, RLS-safe) |
| `billing` | plans, subscriptions, usage, evidence invoices (core) |
| `exchange` | USD↔ZAR rate fetch + cache with provider fallback |
| `crypto` | AES-256-GCM for at-rest repo-token encryption |
| `api`, `middleware`, `admin`, `web`, `docs` | HTTP router, RLS/rate-limit middleware, admin HTML, embedded SPA, this docs site |

## How state is derived

1. **Git engine** clones repos and reads commits, diffs, blame, and lead times.
2. **Sync** pulls issues/PRs from GitHub *and* GitLab into one unified model, and auto-progresses
   issues from linked PR state (open → in progress, merged → done).
3. **LLM** reads diffs to estimate semantic difficulty and synthesize status — never to rank people.
4. **Metrics** compute cycle time (DORA) and involvement *texture*; reporting answers natural-language
   questions over a SELECT-only, RLS-scoped query path.

## Request lifecycle

The middleware chain (outermost → innermost) is:

```
Recoverer → Logger → RateLimit(300/min per IP) → CORS → AuthContext → mux
```

Org-scoped routes additionally wrap `RequireAuth` then `OrgScope` (active org from the `X-Org-ID`
header) and run their reads inside `db.WithOrg(ctx, orgID, fn)`, which opens a transaction and runs
`SET LOCAL app.current_org` before any query.

## Tenancy & security

Every org-scoped table has a row-level-security policy `org_id = current_setting('app.current_org')::uuid`.
Because the setting is injected by `db.WithOrg`, isolation is enforced by the database — not just app
code — and proven by an automated cross-org test (`internal/store/rls_test.go`). Super-admin access
is audited. See [Security](/docs/security).

## Open core + Enterprise

The core is **AGPL-3.0**. The `ee/` directory (Paystack billing, cross-org admin) is commercial and
compiled only with `-tags ee`; the default OSS build links no-op stubs in its place. Billing routes
are *also* runtime-gated by `billing.enabled`. Default builds are fully open and self-hostable.
