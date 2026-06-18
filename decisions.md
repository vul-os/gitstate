# gitstate — Architecture & Product Decisions

Decisions are grounded in the wedge (see [`roadmap.md` §0](./roadmap.md)). Format: **Decision →
Why → Consequence**. Agents: when a choice isn't covered here, choose the option that best serves
*derived-not-entered*, *measure-work-not-workers*, and *evidence-with-visible-gaps* — then append a
new entry.

## Product

**P1. Two truth-modes, one board.** Dev work's source of truth is **git** (derived state); non-dev
work's source of truth is **the tool** (manual). → Honest: we only claim "derived truth" where it's
real. We never infer marketing/design "contribution" from git. → Non-dev tasks are first-class native
records; dev issues are projections of git reality.

**P2. Involvement, never a score.** Per-person/per-project contribution is shown as **texture across
multiple dimensions** (features shipped, **review load**, ownership, spread, active/dormant) — never a
single number, never a bonus formula. → Review/ownership are counted so seniors/mentors aren't zeroed.
→ "Who is *relevant*" (routing) is supported; "who is *worth most*" (ranking) is deliberately not.

**P3. Estimates are evidence, not guesses.** Effort comes from **LLM reading the diff** (difficulty),
not lines/commits. Forecasts are calibrated from observed cycle time. → No story-point input field as
the source of truth. → Every estimate links to its git evidence.

**P4. Evidence billing with visible gaps.** Invoices are backed by git activity; work git can't see
(meetings/research) is **flagged for a human to add**, never auto-invented. → Defensible to clients.
→ We will *under*-count rather than fabricate.

**P5. Agent-native from day one.** `agent_runs` is a first-class unit (goal, diff, tests, accept/reject,
cost). Humans tracked as the *oversight* layer (span, intervention rate, review catch-rate) — to
*inform*, not formula-rank. → Survives the shift to agent-written code; incumbents' ticket model doesn't.

**P6. Free stakeholders.** Billing is per **builder**; stakeholders/clients/viewers are free. → The
seat-tax-killer; structurally un-matchable by per-seat incumbents. → `org_members.role = stakeholder`
never counts toward seat billing.

## Architecture

**A1. Go backend, single binary.** → Best OSS self-host story; strong concurrency for repo sync + LLM
fan-out; cheap to run (margin). → Web build embedded into the binary for prod (`embed`).

**A2. Postgres (Neon) + RLS for tenancy.** Every org-scoped table has `org_id` and an RLS policy
`org_id = current_setting('app.current_org')::uuid`. Request middleware does `SET LOCAL app.current_org`
inside the tx. → Isolation enforced in the DB, not just app code. → Super-admin uses a separate audited
service path, not ambient bypass.

**A3. pgx + hand-written SQL (no heavy ORM).** → Predictable, queryable (the reporting wedge wants real
SQL), reviewable by OSS contributors. → `internal/store` holds queries; `sqlc` optional later.

**A4. Forward-only migrations.** `YYYYMMDD_NNN_name.sql`, no up/down. `reset` = drop+reapply (dev only,
refused on prod). → Matches Supabase ergonomics the team wanted; rollbacks are new forward migrations.
→ Checksums detect edited-after-apply.

**A5. JWT access + rotating refresh tokens.** Short-lived access (15m) signed HS256/EdDSA; refresh
(30d) stored hashed, **rotated on use**, reuse-detection revokes the family. → Stateless API, revocable
sessions. → `refresh_tokens` table with `family_id`, `replaced_by`.

**A6. OAuth is config-gated.** Google/Microsoft enabled only if client id/secret present in config; the
login page renders only the providers that are configured. → Self-hosters opt in; no dead buttons. →
`config.auth.providers.{google,microsoft}.enabled` derived from secret presence.

**A7. Open core + EE in one repo (GitLab model).** Core = **AGPL-3.0**. `ee/` = commercial license,
behind Go build tag `ee` and a runtime license check. Billing/Paystack and cross-org super-admin live
in `ee/`. → OSS users self-host fully (incl. their own billing-less use); paid features are clearly
fenced. → Default build excludes `ee`; cloud build includes it.

**A8. Bill USD, charge ZAR.** Prices are defined in **USD** (the global anchor); Paystack charges in
**ZAR** using a cached exchange rate captured **at charge time** and stored on the invoice. → Protects
margin against FX drift; invoice shows both currencies + the rate used. → `exchange_rates` cached with
TTL + provider fallback; never charge on a stale-beyond-TTL rate without refresh.

**A9. Admin as server-rendered HTML.** Super-admin "super pages" are Go `html/template` + htmx + SSE for
realtime — not part of the React app. → Smaller attack surface, no API token in a browser SPA for
god-mode, fast to build, works without the frontend. → Lives in `internal/admin` (+ `ee/admin`).

**A10. Shared root env, VITE_ prefix split.** One `.env`/`.env.dev` at root holds backend secrets
(unprefixed) and frontend-public vars (`VITE_`). Vite `envDir` points at root; it only exposes `VITE_*`.
→ One file to manage; secrets never leak to the bundle (Vite ignores unprefixed). → `.env.example`
documents both halves.

**A11. Config = file + env overlay.** `config.yaml` (committed example) for structure/flags; secrets via
env. Env wins. → Twelve-factor friendly, but readable structured config for self-hosters.

**A12. Deploy: fly.io first, portable everywhere.** `deploy/fly.toml` primary; multi-stage `Dockerfile`
(build web + go, embed, scratch/distroless final), `docker-compose.yml` (app + optional local PG),
systemd unit, bare-binary docs. → Neon as managed PG; compose offers local PG for pure self-host. →
"other ways to deploy" satisfied without lock-in.

## Security

**S1. RLS is the tenancy boundary.** App bugs must not cross orgs because the DB won't allow it. Tests
assert cross-org reads return zero rows.

**S2. Super-admin is audited, never ambient.** Cross-org access goes through `ee/admin` with an
`audit_log` entry per org touched; the service role is separate from request-scoped roles.

**S3. Secrets only in env, never committed.** `.env*` (except `.example`) gitignored. Paystack/OAuth/JWT
keys never in `config.yaml`.

**S4. Webhooks verified.** Paystack webhooks verified by signature; idempotent via `paystack_events`.
