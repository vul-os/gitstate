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
| 1 | Backbone (Go: config/db-RLS/router) + Web shell (Tailwind/routing/auth UI) | ✅ done |
| 2 | Auth (JWT+refresh+argon2) · Exchange (USD↔ZAR) · Web auth flows | ⏳ dispatched |
| 2b | Identity/tenancy: oauth (google/ms) · orgs/members/invites · web org UX | ⬜ |
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

## Wave 1 contracts (next waves build on these)
- `config.Load() (*Config, error)` — fields: `App{Name,Env,PublicURL,HTTPAddr}`, `Database{URL,MaxConns,RLS}`,
  `Auth{JWTSigningKey,AccessTokenTTL,RefreshTokenTTL,Password,Providers{Google,Microsoft{ClientID,ClientSecret,Enabled}}}`,
  `Git`, `LLM{Provider,Model,AnthropicAPIKey}`, `Billing{...,Plans[]}`, `Admin{SuperAdminEmails,Realtime}`.
  OAuth `.Enabled` is DERIVED (id!="" && secret!="").
- `db.New(ctx, *config.Config) (*DB, error)` · `(*DB).WithOrg(ctx, orgID string, fn func(pgx.Tx) error) error`
  (begins tx → `SET LOCAL app.current_org` → fn → commit) · `(*DB).Ping` · `(*DB).Pool()` · `(*DB).Close()`.
- Router: stdlib `net/http.ServeMux` (Go 1.22 patterns) in `internal/api/router.go`. `GET /healthz`, `GET /api/config`.
  Middleware in `internal/middleware`: `Logger, Recoverer, CORS, AuthContext (stub), Chain`.
- **Route-wiring rule (avoid router.go collisions):** feature packages expose `RegisterRoutes(mux, deps)`;
  ONLY the orchestrator edits `router.go`. Each agent writes its OWN handler file (`internal/api/<feature>.go`)
  and OWN store file (`internal/store/<feature>.go`).
- Web: Tailwind v4 (`@tailwindcss/vite`), `envDir:'..'`, brand tokens `gs-teal/gs-indigo/gs-base`.
  Routes `/login /signup / /projects /settings`. `web/src/lib/api.js` (token in localStorage `gs_access_token`,
  `Authorization: Bearer`), `web/src/lib/auth.jsx` + `useAuth.js`. Consumes `/api/config` for OAuth gating.

## Log
- W0: foundation laid; migrate tool builds + smoke-tested; deps pre-added; base schema w/ RLS.
- W1: Go backbone (config/db-RLS/router/main) + web shell (Tailwind/routing/branded auth UI). Integrated green
  (go build+vet+tidy, npm run build all clean). Committed.
