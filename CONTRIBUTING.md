# Contributing to gitstate

## Dev setup

```bash
cp .env.example .env.dev          # point DATABASE_URL at a Neon dev branch
go run ./cmd/migrate up
go run ./cmd/gitstate             # backend on :8080
cd web && npm install && npm run dev   # frontend on :5173
```

Requires Go 1.25+, Node 20+, and a Postgres (Neon recommended).

## Layout

```
cmd/        gitstate (server), migrate (migrations), billsim (viability sim), seed (demo data)
internal/   open-core Go (config, db, store, auth, oauth, git, sync, llm, billing, metrics,
            report, capacity, admin, exchange, crypto, api, middleware, web)
ee/         Enterprise Edition (commercial license, build tag `ee`): billing (Paystack), admin (cross-org)
migrations/ forward-only SQL — YYYYMMDD_NNN_name.sql
web/        React 19 + Vite + Tailwind (JSX only)
```

## Conventions

- **Frontend is JSX, not TypeScript.** No `.tsx`, no TS config.
- **Backend is Go idioms** — small packages, stdlib `net/http` ServeMux routing (Go 1.22 patterns), `log/slog`.
- **Feature packages do NOT edit `internal/api/router.go` or `cmd/gitstate/main.go`.** Each feature exposes a
  `Register<Feature>Routes(mux, db, cfg)` (or a `Start`/`Handler`) and the router wires it. This keeps parallel
  work conflict-free.
- **Migrations are forward-only.** A rollback is a new migration. `migrate reset` is dev-only. Never edit an
  applied migration (checksums will reject it) — add a new one.
- **Multi-tenancy is enforced by Postgres RLS.** Org-scoped reads/writes must run inside `db.WithOrg(ctx, orgID, …)`,
  which sets `app.current_org`. Don't rely on app-level filtering alone.
- **Secrets only in env** (`.env*`, gitignored), never in `config.yaml` or code.

## The wedge (keep it honest)

Changes must respect the product disciplines in [`decisions.md`](./decisions.md):
derived-not-entered, involvement-as-texture (never a single score / bonus formula),
evidence-billing with visible gaps, free stakeholders, agent-native. If a change can't honor these,
open an issue to discuss before building.

## Building

```bash
go build ./...            # OSS build
go build -tags ee ./...   # Enterprise Edition
go vet ./... && go test ./...
cd web && npm run build && npm run lint
```

## Tests

`go test ./...` runs without a database (the RLS isolation test skips when `DATABASE_URL` is unset).
Set `DATABASE_URL` to a throwaway Postgres to run the full suite including the cross-org RLS proof.
