<!-- title: API Reference | order: 41 | category: Reference | summary: Every REST endpoint, grouped by area, with auth and org-scope conventions. -->

# API Reference

The REST API is served from the same binary as the UI. Routing uses stdlib `net/http.ServeMux` with
Go 1.22 method patterns.

## Conventions

- **Auth:** send `Authorization: Bearer <accessToken>` on protected routes. Access tokens are
  short-lived JWTs; refresh via `POST /auth/refresh`.
- **Org scope:** org-scoped routes require an `X-Org-ID: <org-id>` header. The `OrgScope` middleware
  verifies membership and runs reads under that org's RLS context.
- **Roles:** `owner`, `admin`, `member` (builders) and `stakeholder` (free). See [Concepts](/docs/concepts).
- Feature routes are skipped if the server boots without a database (dev-without-DB).

## Public (no auth)

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/api/config` | Public config: which OAuth providers are enabled, billing charge currency |
| `GET` | `/api/plans` | Public pricing/plan ladder |
| `GET` | `/api/docs` | List doc pages (nav index, no content) |
| `GET` | `/api/docs/{slug}` | One doc page (markdown content) |

## Auth

| Method | Path | Description |
|---|---|---|
| `POST` | `/auth/signup` | Create account → `{accessToken, refreshToken, user}` |
| `POST` | `/auth/login` | Email + password login → tokens + user |
| `POST` | `/auth/refresh` | Rotate refresh token, issue a new access token |
| `POST` | `/auth/logout` | Revoke session (204) |
| `GET` | `/auth/oauth/{provider}/start` | Begin Google/Microsoft OAuth (CSRF state cookie) |
| `GET` | `/auth/oauth/{provider}/callback` | OAuth callback; redirects with tokens in the URL fragment |

## Orgs & members

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/orgs` | List orgs the user belongs to |
| `POST` | `/api/orgs` | Create an org |
| `GET` | `/api/orgs/{id}/members` | List members (org-scoped) |
| `POST` | `/api/orgs/{id}/members` | Invite a member (role incl. free `stakeholder`) |
| `PATCH` | `/api/orgs/{id}/members/{userId}` | Change a member's role |
| `DELETE` | `/api/orgs/{id}/members/{userId}` | Remove a member |
| `POST` | `/api/invites/accept` | Accept an invite token |

## Projects

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/projects` | List projects (org-scoped) |
| `POST` | `/api/projects` | Create a project |

## Repos & issues (sync)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/repos` | List connected repos |
| `POST` | `/api/repos` | Connect a GitHub/GitLab repo (`platform`, `fullName`, `token`, optional `baseURL`) |
| `POST` | `/api/repos/{id}/sync` | Trigger a sync (pull issues/PRs, auto-progress) |
| `GET` | `/api/issues` | List issues (synced + native) |
| `POST` | `/api/issues` | Create a native (manual) issue |
| `PATCH` | `/api/issues/{id}` | Update an issue |

See [Connecting repos](/docs/connecting-repos).

## Metrics

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/metrics/cycle-time` | DORA cycle-time data (lead time, review time) |
| `GET` | `/api/metrics/involvement` | Involvement texture per user/project (no score) |
| `POST` | `/api/metrics/estimate/{prId}` | LLM diff-difficulty estimate for a PR |

## Reports

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/reports/dashboard` | Project-state rollup (`?synthesize=true` adds LLM prose) |
| `GET` | `/api/reports/burndown` | Burndown (`?projectId=&days=`) |
| `POST` | `/api/reports/query` | NL→report: returns rows **and** the SELECT it ran (SELECT-only, RLS-safe) |

See [Metrics & reporting](/docs/metrics-and-reporting).

## Capacity

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/leave` | List leave entries |
| `POST` | `/api/leave` | Request leave (starts `pending`) |
| `PATCH` | `/api/leave/{id}` | Approve / reject leave |
| `GET` | `/api/availability` | Get availability |
| `PUT` | `/api/availability` | Set availability (weekly hours, working days) |
| `GET` | `/api/time-entries` | List time entries (git-derived or manual) |
| `POST` | `/api/time-entries` | Add a time entry |
| `GET` | `/api/capacity` | Effective capacity = availability − approved leave |

## Billing

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/billing/plans` | Plan ladder |
| `GET` | `/api/billing/subscription` | Current subscription |
| `GET` | `/api/billing/usage` | Realtime usage rollup |
| `GET` | `/api/billing/invoices` | List invoices |
| `GET` | `/api/billing/invoices/{id}` | One invoice (USD + ZAR + FX rate, evidence lines) |

### Paystack (EE — `-tags ee` + `billing.enabled`)

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/billing/checkout` | Draft invoice, stamp ZAR + FX, return `authorization_url` |
| `GET` | `/api/billing/verify/{ref}` | Verify a transaction reference |
| `POST` | `/api/billing/webhook` | Paystack webhook (HMAC-SHA512 verified, idempotent) |

In the OSS build these routes are no-op stubs. See [Billing](/docs/billing).

## Super-admin console (server-rendered HTML)

Not part of the SPA — these return HTML and require a super-admin (email allowlist or
`users.is_super_admin`).

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin` | Analytics dashboard |
| `GET` | `/admin/users` | Users management |
| `GET` | `/admin/orgs` | Orgs management |
| `GET` | `/admin/events` | SSE realtime event stream |
| `POST` | `/admin/users/{id}/promote` | Promote to super-admin |
| `POST` | `/admin/users/{id}/demote` | Demote from super-admin |

### EE cross-org (audited — `-tags ee`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/orgs/{id}` | Cross-org drilldown (writes an `audit_log` entry) |
| `GET` | `/admin/revenue` | MRR / revenue dashboard |

Every cross-org access is audited — see [Security](/docs/security).
