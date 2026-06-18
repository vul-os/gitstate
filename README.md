<p align="center"><img src="assets/logo.svg" alt="gitstate" height="64"></p>

<h1 align="center">gitstate</h1>

<p align="center"><b>The project tracker nobody updates by hand.</b><br>
gitstate reads your repos and <i>derives</i> true project state, effort, and (for billing teams)
the invoice from git itself — built for a world where agents write the code and humans supervise.</p>

---

## Why it exists

Every current tool (Jira, Linear, ClickUp, ZenHub) is a **manually-maintained fiction** sitting
next to git. Estimates are ~30% wrong (and have been for 40 years), velocity is gamed the moment
it's a target, and timesheets are reconstructed Friday from memory. **Git is the real ledger.**
gitstate stops asking humans to invent numbers — it observes them from git — and makes whatever
fiction remains *explicit*.

Five disciplines constrain everything:

- **Derived, not entered** — state comes from git (merged = done, PR open = in progress). Nobody maintains tickets.
- **Measure work, not workers** — contribution is shown as *texture across dimensions including review*, never a single score, never a bonus formula.
- **Evidence-based, gaps visible** — effort/billing is backed by git; the part git can't see (meetings, research) is *flagged for a human to fill*, never invented.
- **Free stakeholders** — billing is per *builder*; clients/viewers are free.
- **Agent-native** — agent runs are first-class; humans are the oversight layer.

## Features

- **Git engine** — clone/diff/blame reading; agent-commit detection; DORA lead/cycle time.
- **GitHub + GitLab sync** — two-way issues/PRs, unified, with **auto-progress** (issue → in_progress/done from linked PR state).
- **LLM diff-difficulty** — semantic effort estimation that judges the *change*, not line counts.
- **Two truth-modes** — dev work derived from git; non-dev work tracked manually — shown honestly.
- **Dashboards & NL→report** — cycle-time, involvement-as-texture, burndown, and natural-language queries (RLS-safe, SELECT-only).
- **Capacity & PTO** — availability-aware planning (capacity = availability − approved leave).
- **Billing (EE)** — Paystack, **billed in USD / charged in ZAR** at capture-time FX, evidence invoices with flagged gaps.
- **Super-admin console** — server-rendered HTML + realtime SSE; cross-org access is audited.
- **Open-core + EE** — AGPL-3.0 core, commercial `ee/` (Paystack billing + cross-org admin), GitLab-style build tags.

## Architecture

- **Backend** — Go (single static binary), Postgres (Neon) with **row-level security** for tenant isolation.
- **Frontend** — React 19 + Vite + Tailwind (JSX), served standalone in dev or embedded in the Go binary for prod.
- **Build tags** — default build is OSS; `go build -tags ee` enables the Enterprise Edition.

## Self-host quickstart

```bash
git clone <repo> gitstate && cd gitstate

# 1. Configure (one shared env for backend + frontend)
cp .env.example .env.dev          # point DATABASE_URL at your Neon branch; set JWT_SIGNING_KEY etc.

# 2. Database (Supabase-style forward-only migrations)
go run ./cmd/migrate up           # uses .env.dev
go run ./cmd/seed                 # optional: populate a demo org

# 3. Run
go run ./cmd/gitstate             # API + admin on :8080
cd web && npm install && npm run dev   # web on :5173 (Vite), proxies to the API
```

Single-binary / Docker:

```bash
make build         # builds web, embeds it, builds the OSS binary
make build-ee      # same, with Paystack billing + cross-org admin (-tags ee)
docker compose up  # app (+ optional local postgres via the `local-db` profile)
# fly.io: see deploy/fly.toml
```

## Migrations

Forward-only, Supabase-style — `migrations/YYYYMMDD_NNN_name.sql` (no up/down; a rollback is a new migration).

```bash
go run ./cmd/migrate new <name>   # scaffold
go run ./cmd/migrate up           # apply pending
go run ./cmd/migrate status       # applied + pending
go run ./cmd/migrate reset        # drop & re-apply (dev only)
```

## Billing viability

`go run ./cmd/billsim` simulates the pricing model (cohorts, conversion, churn, FX, LLM COGS) and prints
a profitability table. Headline at defaults: loss-making at 100 orgs, **+56.6% gross margin at 1,000**, +60.5%
at 10,000 — with LLM inference cost as the decisive lever (flags any tier it pushes underwater).

## License

- Everything outside `ee/` — **AGPL-3.0** (see [`LICENSE`](./LICENSE)).
- `ee/` (Enterprise Edition: Paystack billing, cross-org admin) — **commercial** (see [`ee/LICENSE`](./ee/LICENSE)).

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) to hack on it and [`docs/security.md`](./docs/security.md) for the security model.
