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
| 2 | Auth (JWT+refresh+argon2) · Exchange (USD↔ZAR) · Web auth flows | ✅ done |
| 2b | Identity/tenancy: oauth (google/ms) · orgs/members/invites · web org UX | ✅ done |
| 3 | Git engine (read, sync, llm, work UI) | ✅ done |
| 4 | Metrics, reporting (NL→report), capacity/PTO, dashboards | ✅ done |
| 5 | Billing (EE): Paystack, USD→ZAR, billsim | ✅ done |
| 6 | Super admin (EE) + security pass | ✅ done |
| 7 | Deploy, OSS, demo seed, web polish | ✅ done |

**BUILD COMPLETE** — all 9 Definition-of-Done checks green (both build tags, vet, test, web build+lint,
billsim, seed/migrate compile, single-binary serves embedded app). 3 migrations, 10 commits.

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
- W2: auth (JWT HS256 access + rotating refresh w/ family reuse-detection, argon2id; /auth/signup|login|refresh|logout;
  RequireAuth + UserFromContext) · exchange (USD↔ZAR: exchangerate-api+openexchangerates, TTL cache, fallback,
  Convert(usdCents)→zarCents, StartRefresher) · web auth lifecycle (refresh-on-401-and-retry, signup strength meter,
  settings account). Orchestrator wired RegisterAuthRoutes→router (DB-guarded) + exchange refresher→main.
  Smoke: /healthz, /api/config OK; no-DB boot returns 404 (no panic). Integrated green. Committed.

## Wave 2 contracts (for 2b)
- `auth.IssueAccessToken(signingKey,userID,email,name,ttl)`, `auth.ParseAccessToken`, `auth.GenerateRefreshToken()`,
  `auth.HashToken`. `middleware.RequireAuth(signingKey)`, `middleware.UserFromContext(ctx) *AuthUser{ID,Email,Name}`.
- store: `CreateUser/GetUserByEmail/GetUserByID`, `InsertRefresh/GetRefreshByHash/RotateRefresh/RevokeFamily`,
  `NewExchangeStore(pool)`. Feature pkgs expose `Register<Feature>Routes(mux, db, cfg)`; orchestrator wires router.go.
- HTTP auth: signup/login → `{accessToken,refreshToken,user{id,email,name}}`; refresh rotates; logout 204.
  Web stores `gs_access_token`/`gs_refresh_token`; access JWT carries sub/email/name (+org_id/role when present).
- W2b: oauth (google+ms, config-gated, CSRF state cookie, find-or-create+link, callback redirect
  `${PublicURL}/login#access=&refresh=`) · orgs/members/invites (`RegisterOrgRoutes`, `OrgScope(pool)` mw reading
  `X-Org-ID`, `OrgFromContext`; free stakeholders) · web org UX (OrgSwitcher, Members page, invite accept,
  oauth-fragment parse, X-Org-ID header on /api/*). Orchestrator wired RegisterOAuthRoutes+RegisterOrgRoutes→router
  (DB-guarded). Integrated green (go build+vet+tidy, npm build+lint clean). Committed.

## Wave 2b contracts (for 3+)
- `middleware.OrgScope(pool)` + `middleware.OrgFromContext(ctx) string`; active org via header `X-Org-ID`.
  Org-scoped feature routes should wrap RequireAuth→OrgScope and run reads in `db.WithOrg(ctx, orgID, ...)`.
- `store` org helpers: ListOrgsForUser, CreateOrg, GetOrg, GetMemberRole, ListMembers, Add/Remove/UpdateMemberRole,
  CreateInvite, GetInviteByTokenHash, AcceptInvite. oauth: FindOrCreateOAuthUser, GetOAuthAccount, LinkOAuthAccount.
- W3: git engine. `internal/git` (Clone/Fetch/WalkCommits/Diff/DiffRange/Blame/LeadTime; is_agent heuristic;
  input-sanitized exec). `internal/sync` (GitHub go-github + GitLab client; SyncRepo; parseIssueRefs; auto-progress:
  open PR→issue in_progress, merged PR→done, merged-wins, never overwrites canonical state). `internal/llm`
  (Provider iface + Anthropic via net/http, model claude-sonnet-4-6; EstimateDifficulty 1–10 + rationale;
  SynthesizeStatus; ErrLLMNotConfigured no-op). Stores: commits, pull_requests, repos, issues, estimates.
  `RegisterSyncRoutes` (/api/repos connect/list/sync, /api/issues list/create-native/patch). Web: /repos connect,
  Board/List/Table, IssueDrawer w/ LLM estimate, two-truth-modes badges, native-issue modal, /projects.
  Orchestrator added minimal /api/projects (store+handler) + wired RegisterProjectRoutes+RegisterSyncRoutes→router.
  Integrated green (build/vet/tidy, web build+lint, boot-smoke). Committed.
  NOTE: repo tokens are NOT persisted — clients re-supply on sync/patch (a Wave 6 security item: encrypted token store).

## Wave 3 contracts (for 4+)
- git: `git.WalkCommits/Diff/DiffRange/Blame/LeadTime`, store `UpsertCommit/ListCommits(Tx)`, `UpsertPR/ListPRs(Tx)/GetPR`.
- sync: `RegisterSyncRoutes`; issues store `UpsertIssue/ListIssues/SetDerivedState/CreateNativeIssue/GetIssue`.
- llm: `llm.New(cfg)`, `EstimateDifficulty(ctx,diff,meta)`, `SynthesizeStatus`; estimates store `SaveEstimate/GetEstimateForPR|Issue`.
- projects: `RegisterProjectRoutes`, store `ListProjects/CreateProject` (org-scoped via WithOrg).
- W4: metrics (`RegisterMetricsRoutes`; ComputeCycleTimes/Involvement[texture, no score field anywhere]/EstimateForPR;
  /api/metrics/cycle-time|involvement|estimate) · reporting (`RegisterReportRoutes`; Dashboard/Burndown/AnswerQuery
  NL→report w/ 4-layer safety: constrained prompt → SELECT-only validateSQL → read-only tx+5s timeout → RLS via WithOrg;
  /api/reports/dashboard|burndown|query; added internal/llm/complete.go) · capacity (migration 20260618_002_capacity.sql:
  leave_entries/availability/time_entries +RLS; EffectiveCapacity=available−approvedLeave; /api/leave|availability|
  time-entries|capacity) · web (Dashboard home, /cycle-time SVG charts, /involvement texture cards "not a productivity
  score", NL query box w/ SQL shown, /capacity editor, burndown). Wired 3 registrars→router. Integrated green
  (build/vet/tidy, web build+lint, boot-smoke; 2 migrations present). Committed.
- W5: billing. internal/billing core (`RegisterBillingRoutes`, runtime-gated cfg.Billing.Enabled; GenerateInvoice w/
  per-builder seats [stakeholders free P6] + git-evidence lines + is_estimated/confirmation_required gaps P4;
  CurrentUsage/PlanCeiling; 13 store funcs in store/billing.go) · ee/billing (BUILD-TAGGED: paystack.go //go:build ee
  real checkout/webhook/verify, charges ZAR via exchange.Convert, HMAC-SHA512 webhook verify constant-time + idempotent;
  stub.go //go:build !ee no-op; ee/billing/LICENSE.md) · cmd/billsim (viability sim: -34%@100, +56.6%@1k, +60.5%@10k;
  LLM cost the lever, ⚠ flags tiers underwater) · web Billing page (USD-billed/ZAR-charged shown, stakeholders-free
  explicit, evidence/estimated invoice lines, Paystack checkout+return). Wired RegisterBillingRoutes +
  eebilling.RegisterPaystackRoutes→router. Integrated: `go build ./...` AND `go build -tags ee ./...` both green,
  vet+web+billsim+boot-smoke clean. Committed.
- EE build: default = OSS (Paystack stub). Cloud = `go build -tags ee`. Billing routes also runtime-gated by Billing.Enabled.
- W6: super-admin (internal/admin: `RequireSuperAdmin(cfg,db)` [email allowlist or users.is_super_admin],
  `RegisterAdminRoutes`; server-rendered html/template+htmx+SSE: /admin analytics, /admin/users, /admin/orgs,
  /admin/events SSE realtime; store/admin.go global aggregates) · ee/admin (BUILD-TAGGED: /admin/orgs/{id} cross-org
  drilldown + /admin/revenue MRR, EVERY access → audit_log via WriteAudit; stub for !ee) · security (store/audit.go
  WriteAudit, middleware/ratelimit.go RateLimit+AuthRateLimit, internal/crypto AES-256-GCM, migration 003 repos.token_encrypted,
  store/repo_tokens.go, store/rls_test.go [S1 cross-org=0 rows, skips w/o DB], docs/security.md). Wired admin+eeadmin routes
  + RateLimit(300) into chain. Both build tags + vet + go test + boot-smoke green. 3 migrations. Committed.
  NOTE: collision auto-resolved — EE agent created internal/admin/admin.go w/ RequireSuperAdmin; HTML agent kept it,
  added routes.go (same signature). No duplicate symbol.
- W7: deploy (internal/web go:embed Handler w/ SPA fallback+dev placeholder; deploy/fly.toml[jnb]; multi-stage
  Dockerfile -tags ee; docker-compose +optional local pg; systemd; Makefile) · OSS (LICENSE AGPL-3.0 [fetched],
  ee/LICENSE commercial, README rewrite, CONTRIBUTING, SECURITY — orchestrator wrote these after the OSS agent
  hit a content filter on the license text) · cmd/seed (Acme Dev Shop demo: 5 users incl stakeholder+agent,
  2 repos gh+gl, git+native issues, merged/open PRs, agent commits, cycle/involvement/estimates, leave/capacity;
  login demo@gitstate.dev/demo1234; -reset flag) · web polish (Welcome landing, 404, empty/loading states,
  base:'/'). Wired web.Handler() catch-all → router. FINAL VERIFY: 9/9 DoD checks green; single binary serves
  embedded SPA (/ 200, /board SPA-fallback 200, /assets 200). Committed.
