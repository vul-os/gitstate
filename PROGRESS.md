# gitstate — Build Progress (live)

Peekable status for the autonomous build. Opus orchestrates; Sonnet agents build disjoint
packages per wave. Opus integrates + commits at each wave boundary (agents do NOT commit, to
avoid parallel git-index races). Waves & scope: see [`roadmap.md` §4](./roadmap.md).

**Mode:** autonomous, ~15-min wakeups, target ~8h.
**Started:** 2026-06-18.

## Status

| Wave | Scope | State |
|---|---|---|
| 0 | Foundation: skeleton, docs, logo, env/config, migration tool, base schema | ✅ done |
| 1 | Backbone (Go) + Web shell | ⏳ dispatched |
| 2 | Identity & tenancy (oauth, orgs, RLS scoping, exchange) | ⬜ |
| 3 | Git engine (read, sync, llm, work UI) | ⬜ |
| 4 | Metrics & reporting | ⬜ |
| 5 | Billing (EE): Paystack, USD→ZAR, billsim | ⬜ |
| 6 | Super admin (EE) + security pass | ⬜ |
| 7 | Deploy & OSS hygiene | ⬜ |

## Contracts (so parallel agents stay compatible)
- Module: `github.com/exo/gitstate`. Deps pre-installed in go.mod — **import freely, do NOT `go get`**.
- Config struct lives in `internal/config`; load via `config.Load()`.
- DB pool + RLS session helper in `internal/db`; every request tx does `SET LOCAL app.current_org`.
- Auth issues JWT access + rotating refresh; claims carry `user_id`, `org_id`, `role`.
- HTTP router + middleware in `internal/api` + `internal/middleware`; handlers under `internal/api`.
- Web app in `web/`, React JSX (NO tsx), Tailwind; API base from `VITE_API_BASE_URL`.

## Log
- W0: foundation laid; migrate tool builds + smoke-tested; deps pre-added; base schema w/ RLS.
