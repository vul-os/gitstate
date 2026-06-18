# gitstate — Build Roadmap

> **The project tracker nobody updates by hand.** gitstate reads your repos and *derives*
> true project state, effort, and (for billing teams) the invoice from git itself — built for
> a world where agents write the code and humans supervise.

This roadmap is the single source of truth for *what* we build and *in what order*. Architectural
and product rationale lives in [`decisions.md`](./decisions.md). Live build status lives in
[`PROGRESS.md`](./PROGRESS.md).

---

## 0. The Wedge (why every feature exists)

Current tools (Jira, Linear, ClickUp, ZenHub) are a **manually-maintained fiction** sitting next to
git. Estimates are ~30% wrong (and have been for 40 years), velocity is gamed the moment it's a
target, and timesheets are reconstructed Friday from memory. **Git is the real ledger.** gitstate's
job: **stop asking humans to invent numbers — observe them from git**, and make whatever fiction
remains *explicit*.

Three disciplines that constrain every decision:

1. **Derived, not entered.** State comes from git (merged = done, PR open = in progress). Nobody
   maintains tickets.
2. **Measure work, not workers (by formula).** Involvement/contribution is shown as *texture across
   multiple dimensions including review* — never a single score, never a bonus formula.
3. **Evidence-based, gaps visible.** Effort/billing is backed by git; the part git can't see
   (meetings, research) is *flagged for a human to fill*, never silently invented.

**Beachhead ICP:** client-billing dev shops (agencies/consultancies) — acute pain, budget today.
**Expansion:** scaling multi-repo teams → agent-native PM.

---

## 1. Stack & Topology

| Layer | Choice | Notes |
|---|---|---|
| Backend | **Go 1.25** | single static binary = best self-host story; great concurrency for repo sync + LLM fan-out |
| Frontend | **React 19 + Vite + Tailwind, JSX (no TSX)** | lives in `web/` |
| Admin | **Server-rendered HTML** (Go `html/template` + a little htmx) | `internal/admin` + `ee/admin`; "super html pages" for analytics/users/orgs/billing |
| DB | **Neon (Postgres)** | RLS for multi-tenant isolation |
| Auth | **Internal JWT + refresh tokens**, optional Google/Microsoft OAuth | OAuth shown on login only if configured |
| Payments | **Paystack** | bill in **USD**, charge in **ZAR**; exchange-rate API + tables |
| Cloud | **fly.io** primary; Docker + compose + bare binary alternates | `deploy/` |
| License | **Open core + EE in same repo** (GitLab model) | core = AGPL-3.0; `ee/` = commercial license, build-tagged |

### Repo layout
```
gitstate/
├── cmd/
│   ├── gitstate/        # main API + admin server
│   ├── migrate/         # Supabase-style migration runner (up / reset / new / status)
│   └── billsim/         # billing viability simulator (runs simulated numbers)
├── internal/            # open-core Go
│   ├── config/          # config.yaml + env loader; feature flags (OAuth providers)
│   ├── db/              # pgx pool, RLS session helpers
│   ├── store/           # data access (queries)
│   ├── auth/            # JWT issue/verify, refresh rotation, password
│   ├── oauth/           # Google + Microsoft providers (config-gated)
│   ├── git/             # deep git reading: clone, walk, diff, blame, history
│   ├── sync/            # GitHub + GitLab issues/PRs two-way sync
│   ├── llm/             # diff-difficulty sizing, status synthesis
│   ├── billing/         # plans, subscriptions, usage, invoices (core abstractions)
│   ├── exchange/        # USD↔ZAR rate fetch + cache
│   ├── api/             # REST handlers, router
│   ├── admin/           # super-admin HTML pages (analytics/users/orgs/billing realtime)
│   └── middleware/      # auth, RLS context, org scoping, rate limit
├── ee/                  # ENTERPRISE — separate LICENSE, build tag `ee`
│   ├── billing/         # Paystack integration, real charging, ZAR settlement
│   └── admin/           # cross-org super-admin, revenue dashboards
├── migrations/          # YYYYMMDD_NNN_name.sql  (forward-only, no up/down)
├── web/                 # Vite React JSX + Tailwind app
├── deploy/              # fly.toml, Dockerfile, docker-compose.yml, systemd
├── assets/              # logo, brand
├── .env / .env.dev / .env.example   # SHARED backend + frontend (VITE_ prefix for FE)
├── config.example.yaml
├── roadmap.md / decisions.md / PROGRESS.md
```

---

## 2. Data Model (high level)

Tenant root is **organization**. Everything org-scoped carries `org_id` and is protected by RLS.

- **Identity:** `users`, `oauth_accounts`, `refresh_tokens`, `organizations`, `org_members` (role: owner/admin/member/**stakeholder**=free/billing), `super_admins`
- **Projects/work:** `repos` (github|gitlab, connection), `projects`, `issues` (synced + native), `pull_requests`, `commits`, `work_items`, `task_files` (md in repo)
- **Derived/metrics:** `cycle_times`, `effort_estimates` (LLM diff-difficulty), `involvement` (per user/project, multi-dimension), `agent_runs`
- **Billing:** `plans`, `subscriptions`, `usage_events`, `invoices`, `invoice_lines`, `payments`, `exchange_rates`, `paystack_events`
- **Platform:** `audit_log`, `feature_flags`, `schema_migrations`

RLS: every org-scoped table has policy `org_id = current_setting('app.current_org')::uuid`. Super
admins bypass via a `BYPASSRLS`-style service role + audited access path.

---

## 3. Feature Set (tagged: 🎯 wedge · ✅ table-stakes · 🧩 plugin/later · 🔒 EE)

**Git-truth core** 🎯 — derived status, cycle time (DORA), LLM diff-difficulty sizing, evidence
invoice, involvement-as-texture, plan-in-repo (`roadmap.md`/`tasks/`), agent-run tracking.

**Sync** 🎯✅ — GitHub Issues + GitLab Issues two-way, unified board; PR/commit linking; auto-progress.

**Whole-team** ✅ — native (non-repo) tasks (2nd truth-mode: manual), Board/List/Table/Timeline/Calendar
views, custom fields/statuses w/ opinionated defaults, sprints/cycles, milestones, comments/@mentions.

**Planning/people** ✅ — capacity & availability (PTO feeds capacity), time tracking (git-derived for
devs, manual otherwise), calendar integration.

**Reporting** 🎯 — queryable data + NL→report; dashboards/burndown; LLM status synthesis & triage.

**Billing** 🔒 — Paystack, USD-bill/ZAR-charge, exchange rates, realtime billing model, predicted-billing
simulator, plan ladder (Free/Hobby/Pro/Team/Scale/Ent), free stakeholder seats.

**Admin** 🔒 — super HTML dashboards: analytics, users, orgs, realtime billing/revenue, cross-org access.

**Platform** ✅ — open-source self-host, API+webhooks+queryable store, RBAC, SSO (EE), web+mobile-web.

**Deliberately NOT core:** deep docs (Notion), whiteboard, CRM, full HR, **accounting → integrate Xero,
never build**.

---

## 4. Build Waves (for Sonnet agents)

Each wave = parallel agents on **disjoint directories** to avoid collisions. Opus (orchestrator) owns
foundation + contracts + integration + commits. Every wave ends green-build + committed.

### Wave 0 — Foundation (Opus, done first)
- [x] Repo skeleton, Go module, frontend → `web/`
- [x] `roadmap.md`, `decisions.md`, logo, env/config, **migration tool**, **base schema**, config loader

### Wave 1 — Backbone (parallel)
- **A1 db/store/migrate** — pgx pool, RLS session helper, `store` query layer over base schema, seed.
- **A2 auth** — JWT (access+refresh w/ rotation), password hash (argon2), middleware, `/auth/*` routes.
- **A3 config+server** — `cmd/gitstate` HTTP server, router, graceful shutdown, healthz, CORS, config wiring.
- **A4 web shell** — Tailwind install+config, app shell, routing, auth pages, API client, login showing
  configured OAuth providers.

### Wave 2 — Identity & Tenancy (parallel)
- **B1 oauth** — Google + Microsoft providers, config-gated, account linking.
- **B2 orgs/members** — org CRUD, invites, roles incl. free **stakeholder**, RLS scoping middleware.
- **B3 web auth+org UX** — full login/signup/oauth flows, org switcher, member management UI.
- **B4 exchange** — USD↔ZAR rate fetch (provider + fallback), cache table, scheduled refresh.

### Wave 3 — Git Engine (parallel)
- **C1 git read** — clone/fetch, walk commits, diff, blame, history into `commits`/`pull_requests`.
- **C2 sync** — GitHub + GitLab issues/PRs two-way sync; unified `issues`; auto-progress from git.
- **C3 llm** — diff-difficulty sizing, status synthesis (pluggable provider; Anthropic default).
- **C4 web work UI** — board/list/table views, issue detail, project view, two truth-modes.

### Wave 4 — Metrics & Reporting (parallel)
- **D1 derived metrics** — cycle time (DORA), involvement (multi-dimension), effort estimates.
- **D2 reporting/query** — queryable endpoint + NL→report (LLM over schema), dashboards API.
- **D3 web dashboards** — burndown, cycle-time, involvement texture, project state.
- **D4 capacity/planning** — PTO/availability, capacity-aware planning, time tracking.

### Wave 5 — Billing (EE) (parallel)
- **E1 billing core** — plans, subscriptions, usage metering, invoice generation (USD).
- **E2 ee/billing Paystack** — checkout, ZAR charge via exchange rate, webhooks, settlement, retries.
- **E3 billsim** — `cmd/billsim` viability simulator (cohorts, conversion, COGS incl. LLM, ZAR FX).
- **E4 realtime billing model** — live usage→cost, plan ladder enforcement (ceiling not reservation).

### Wave 6 — Super Admin (EE) (parallel)
- **F1 admin HTML** — analytics, users, orgs management (Go templates + htmx, realtime via SSE).
- **F2 ee/admin** — cross-org access (audited), revenue/MRR dashboards realtime.
- **F3 security pass** — RLS policy audit, super-admin audit log, secrets, rate limiting, threat review.

### Wave 7 — Deploy & Polish (parallel)
- **G1 deploy** — fly.toml, Dockerfile (multi-stage, embed web build), docker-compose, systemd, docs.
- **G2 OSS hygiene** — READMEs, LICENSE (AGPL) + ee/LICENSE (commercial), CONTRIBUTING, self-host guide.
- **G3 e2e + seed demo** — seed a demo org with synthetic git history; smoke tests.

---

## 5. Migrations (Supabase-style)
- File format: `migrations/YYYYMMDD_NNN_name.sql` — **forward-only, no up/down**.
- Tool: `go run ./cmd/migrate <cmd>` — `new <name>` · `up` · `status` · `reset` (drop+reapply, dev only) · `version`.
- Tracking table `schema_migrations(version, name, applied_at, checksum)`.
- Environment-aware: loads `.env` (or `--env dev` → `.env.dev`). `reset` refuses on prod env.

---

## 6. Definition of Done
- `go build ./...` green; `cd web && npm run build` green; `go run ./cmd/migrate up` clean on Neon.
- RLS verified: cross-org read returns zero rows. Super-admin access audited.
- `cmd/billsim` outputs a viability table showing profitability at target cohorts.
- Self-host: single binary + Postgres URL boots; fly deploy documented.
- Every feature traceable to a wedge discipline in §0.
