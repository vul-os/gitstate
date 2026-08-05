# Migration notes — the staged port

gitstate was, until the transform, a **multi-tenant Go + Postgres + React SaaS**: tenancy enforced by
PostgreSQL Row-Level Security, JWT auth with rotating refresh tokens, optional Google/Microsoft OAuth,
a git engine reading commits/PRs, LLM diff-difficulty sizing, DORA metrics, a Paystack billing tier
behind an `ee` build tag, a server-rendered super-admin console, and a fly.io deploy.

It is being rebuilt as a **standalone, local-first, peer-to-peer desktop app** (Rust core + Tauri +
React + a headless daemon). The product essence is unchanged; the delivery flips off the cloud and
onto the user's machine. This document explains why a large chunk of Go is *still in the tree*.

## Why the Go server is still here

The Go server is kept **in-tree and compiling** — under `internal/`, `cmd/`, and `migrations/`, with
`go.mod` and `go.sum` — for a **staged port**. Rather than delete it and reimplement from memory, we
port its still-valuable domain logic to Rust one area at a time, using the Go source as the reference
implementation. A Go domain is removed only once its Rust replacement passes parity, in a dedicated
commit.

**Untouchable during the port** (no agent edits these): `internal/**`, `cmd/**`, `migrations/**`,
`go.mod`, `go.sum`. They compile and run exactly as before; they are simply no longer the product's
front door. Two exceptions: the billing/invoicing/accounting/COGS layer, which had no path forward in
a local-first single-tenant app and was excised outright (not staged); and, as of **2026-08-04**, every
package confirmed either fully ported or SaaS-only-and-abandoned was `git rm`'d in a domain-mapped
sweep (decisions.md T11's resolution note has the full table and evidence). What remains under
`internal/`/`cmd/` today is genuinely **not yet ported** (`report`, `calibration`, the semantic-search
files inside `store`) plus the scaffolding those need to keep compiling (`config`, `db`, `crypto`,
`llm`, `gitanalysis`) and two standalone CLIs (`gittrack`, `gitstate-mcp`) — see the table below for
exactly what left and why.

## What was removed vs. kept vs. ported

| Legacy piece | Disposition |
|---|---|
| AGPL `LICENSE`, `ee/` (Paystack billing, cross-org admin) | **Removed.** No multi-tenant service to fence; relicensed MIT OR Apache-2.0 (see [decisions.md](../decisions.md) T10). |
| `Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml` | **Removed.** No hosted deploy target; the daemon binds `127.0.0.1`. |
| `internal/{billing,cogs,accounting,invoicedelivery,invoicepdf,exchange}`, `cmd/billsim` | **Removed outright.** The billing/invoicing/SaaS-viability layer (subscriptions, wallets, client invoices, accounting-provider sync, cloud-cost/COGS reconciliation) has no role in a local-first single-tenant app; not staged, not ported. Gone in git history only. |
| Postgres `migrations/` (root) | **Kept**, needed to provision the `gitstate_test` DB the surviving Go reference (`store`/`report`/`calibration`) still builds and tests against in CI. The Rust store's own forward-only migrations live in `crates/gitstate-store/migrations/` so the two never collide. |

### 2026-08-04 domain-mapped removal — ported and SaaS-only Go, gone

Every `internal/` package and `cmd/` binary was read against its Rust counterpart (or lack of one) and
classified PORTED / SAAS-ONLY / NOT-YET-PORTED. The full evidence table lives in the commit that did
this (and in decisions.md T11's resolution note). Summary:

| Legacy piece | Disposition |
|---|---|
| `internal/git` (walk, diff, blame, lead time) | **Ported, removed.** → `gitstate-git` (git2-rs), extended to blame-survival + SZZ + the six-dimension model. Clone/fetch was deliberately dropped, not lost: the app is local-first and expects repos you already have on disk (`AddRepoReq.remote_url` records metadata only, "until cloned locally"). |
| `internal/sync` (Go forge poller) | **Ported, removed.** → `gitstate-forge` (`gh`/`glab` CLI + REST/GraphQL fallback), local-credentials-only, pull-based (no hosted webhook receiver — see `internal/webhooks` below). |
| `internal/contribution`, `internal/contributors` | **Ported, removed.** → `gitstate-core::derive` (composite scoring, `merge_contributor_identities` union-find) + `gitstate-git::derive_contrib`/`history::collect_contributors`. |
| `internal/metrics` | **Ported, removed.** → `gitstate-core::analytics` + `gitstate-core::health` (DORA, bus factor, review health, quality, involvement). |
| `internal/importer` (Jira, Linear) | **Ported, removed.** → `gitstate-tracker` (`jira.rs`, `linear.rs`, `map.rs`). |
| `cmd/seed`, `cmd/seedgit` | **Ported, removed.** → `gitstate-cli seed` (`crates/gitstate-cli/src/cmd/seed.rs`), which folds demo-org + demo-git-history generation into one command. |
| `cmd/gitstate`, `internal/api` | **Removed, SaaS-only.** The multi-tenant HTTP server and its router; superseded architecturally by `gitstate-daemon`'s own routes (`crates/gitstate-daemon/src/routes/`). No handler here carried logic that wasn't already duplicated in `internal/store`/`internal/report`, which are kept. |
| `internal/admin`, `internal/auth`, `internal/middleware`, `internal/oauth` | **Removed, SaaS-only.** Super-admin console, JWT/password/refresh auth, org-scoped rate-limiting, "Sign in with…" — none of it applies to a single-user local app (T1/A5/A6/A9). |
| `internal/calendar`, `internal/capacity` (package; `store/calendar.go`+`store/capacity.go`+`store/leave.go` stay, see below) | **Removed, SaaS-only.** Google/Microsoft calendar two-way sync and team leave-approval capacity planning assume a multi-person org with a manager approving someone else's time off. |
| `internal/chat` | **Removed, SaaS-only.** Org-scoped tool-calling chat assistant over Postgres; not in T11's ported-domain list, and its "ask your data a question" territory is the same one `internal/report`'s NL→report already owns (kept, see below) — no unique capability is lost. |
| `internal/notifications`, `internal/email`, `internal/webhooks` | **Removed, SaaS-only.** Digest delivery to teammates (SMTP/Slack/webhook) and a GitHub/GitLab webhook *receiver* both assume a publicly reachable multi-tenant server; a local desktop app behind NAT has neither teammates to notify nor an inbound endpoint. |
| `internal/githubapp` | **Removed, SaaS-only.** GitHub App installation tokens were the production alternative to per-user OAuth; superseded by T3 (forge access via the user's own `gh`/`glab` credentials, no gitstate-hosted broker). |
| `internal/web` (Go `embed.FS` SPA server) | **Removed, SaaS-only.** Superseded by `gitstate-daemon::serve_static`. |
| `internal/docs` (in-app markdown docs + `/api/docs`) | **Removed, superseded.** Equivalent (and now more complete) docs already live in `site/docs/` (the product's real docs surface — see the Product-site standard). |
| `internal/jobs` (Postgres job queue) | **Removed, SaaS-only.** Durable multi-org background workers have no counterpart in an on-demand, CLI-driven local sync model. |
| `internal/analytics` (product telemetry: `capture.go`/`geo.go`) | **Removed, SaaS-only.** Privacy-first analytics *about the SaaS product itself* (request capture, IP-hash geo) behind the super-admin console — not git analytics. `analytics.go`'s git-analytics service layer in the same package was redundant with `gitstate-core::analytics`. |
| `internal/llm` (diff-difficulty judging) | **Ported.** → `gitstate-classify` (local LLM + deterministic heuristic + personalization). The Go package itself is **kept in-tree** (not deleted) only because `internal/report`'s still-unported LLM status synthesis depends on it; its multi-provider reselling catalog/gateway (`catalog.go`, `gateway.go`, billing markup) is dead weight bundled in the same package — a candidate for a future non-domain cleanup, not touched here to avoid breaking `report`'s build. |
| `internal/report` (NL→report, dashboard burndown/throughput synthesis) | **Kept — NOT YET PORTED.** No Rust equivalent exists anywhere (`burndown` doesn't appear in `crates/` at all). Real, wanted functionality; do not delete. |
| `internal/calibration` (cohort/curve empirical-Bayes effort calibration) + `store/calibration.go` | **Ported (T11 wave 2, 2026-08-05).** → new `gitstate-calibrate` crate: `cohort.rs`/`curve.rs`/`recompute.rs`, a near-1:1 port of the Go package's own three-file split, with the Go test fixtures ported alongside (not just re-derived). Persistence: migration `0004_calibration.sql` adds `effort_calibration` + `effort_accuracy` tables and five nullable columns on `effort` (`predicted_secs`, `actual_secs`, `cohort_key`, `size_bucket`, `change_type`), plus ten new `Store` trait methods in `crates/gitstate-store` — `org_id`/RLS dropped, same reasoning as wave 1's `agent_runs`. No daemon route or CLI subcommand: neither this wave nor the Go reference has a live caller for this domain today (`RecomputeCalibration`/`CalibratedSecs`/`ListExemplars` are unwired even in Go — their only intended caller was the multi-tenant HTTP server already removed as SaaS-only in the 2026-08-04 sweep). Ported as a library for wave 3 (`context_bundle`) to call for a real `EstimateBrief` instead of a stub, per `docs/PORT-PLAN.md` §4's dependency graph. `internal/calibration` and `store/calibration.go` stay in-tree (kept compiling) until the final cleanup wave. |
| `internal/embed` (local semantic embedder) + `store/search.go` + `store/embeddings.go` | **Ported (T11 wave 4, 2026-08-05).** → new `gitstate-search` crate: `embed.rs`/`fuzzy.rs`/`rrf.rs`/`search.rs`, mirroring the Go source's own module split. **Spike resolved**: SQLite has FTS5 (available with zero `Cargo.toml` change — `libsqlite3-sys`'s `bundled` build already compiles it in; the plan's suggestion of an `fts5` `rusqlite` feature was wrong, no such feature exists) but no `pg_trgm`/trigram function, so the fuzzy fallback is a hand-rolled trigram-Jaccard function matching pg_trgm's symmetric `similarity()` exactly — it does **not** replicate `word_similarity()` (Go's asymmetric substring search for PR titles/commit messages), a stated ranking difference for long targets, not a rounding one (full writeup in `gitstate_search::fuzzy`'s crate doc and decisions.md T11). Persistence: migration `0005_search.sql` adds an FTS5 virtual table (`search_fts`, rebuilt fresh per call rather than trigger-synced) and `work_item_embeddings` (issues only, BLOB-encoded little-endian f32 vectors — SQLite has no `pgvector` column type), plus four new `Store` methods — `org_id` dropped, same reasoning as every prior wave. Wired into `ops::search`/`ops::embed_pending` (the latter now also runs automatically, non-fatally, at the end of `scan_repo`, mirroring Go's post-sync `EmbedPendingIssues` hook), a new `gitstate search <query>` CLI command, and the `search_issues` MCP tool — the exact tool wave 3 named as blocked on this wave; MCP is now 4 of 6 Go tools ported. No daemon HTTP route: `web/` has no search consumer (checked). `internal/embed`, `store/search.go`, `store/embeddings.go` stay in-tree (kept compiling) until the final cleanup wave. |
| `store/agent_runs.go` + `cmd/gittrack`'s `log-run`/`runs`/`whoami` + `cmd/gitstate-mcp` | **Ported (T11 wave 1, 2026-08-05).** → `agent_runs` table + `Store` methods in `crates/gitstate-store` (migration `0003_agent_runs.sql`, `org_id`/RLS dropped — single tenant, nothing to scope), `/api/agent-runs` in `crates/gitstate-daemon` (gated by the existing `AdminAuth`, no new token/scope), and `gitstate-cli`'s `agent log-run`/`agent runs`/`agent whoami`/`mcp` subcommands (`crates/gitstate-cli/src/cmd/{agent,mcp}.rs`) — both former standalone binaries folded in, no sixth binary. `gittrack context`/`pr`/`issues` are **not** included: `context`/`pr` are the separate `context_bundle` domain (next row, ported in wave 3); `issues` is plain work-item listing, a different surface, not ported. The Go source stays in-tree (kept compiling) until the final cleanup wave deletes `internal/store` whole. |
| `store/context_bundle.go` + `cmd/gittrack`'s `context <issue>`/`pr <id>` | **Ported (T11 wave 3, 2026-08-05).** → bundle-assembly types in `gitstate-core::domain` (`IssueContextBundle`, `PrContextBundle`, …), two new read-only `Store` methods (`get_work_item`, `get_effort`), and the assembly logic in `crates/gitstate-daemon/src/ops.rs` (`build_issue_context`/`build_pr_context`), called by `gitstate-cli`'s new `agent context <issue-id>`/`agent pr <id>` subcommands and by the `get_issue`/`get_pr_context` MCP tools (both call `ops` in-process, wave 1's pattern) — no daemon route, since neither the CLI nor MCP need HTTP and no other caller exists (Go's own HTTP server, `internal/api`, was already deleted in the 2026-08-04 sweep, so there is no reference implementation left to serve one either). `codeAreas` is re-derived from `WorkItem.files_touched` via `gitstate_calibrate::cohort::top_dirs_from_paths` (no `task_files` table ported — zero live callers, see `docs/PORT-PLAN.md` §3); `IssueSummary` drops `assigneeId` (`WorkItem` has no assignee field). `EstimateBrief.predicted_secs` is a genuine improvement on the Go behaviour, not a stub: Go's own `predicted_secs` column has no writer in either language (`EstimateForPR`, the function its own comment says populates it, does not exist), so the bundle now calls `gitstate_calibrate::curve::calibrated_secs` live whenever a persisted value is absent, producing a real calibrated number (falling back to the cold-start prior with zero history) instead of porting a column that would read `NULL` forever. Distinct from `gitstate-cli context` (the CRDT-synced *saved working set* feature, T8) — same word, different feature; the new subcommands sit under `agent` (`gitstate agent context`/`gitstate agent pr`) to avoid the collision. `store/issues.go`/`store/pull_requests.go` are still not ported — the bundle reads through `WorkItem` instead, per the plan. `internal/store/context_bundle.go` and `cmd/gittrack` stay in-tree (kept compiling) until the final cleanup wave. |
| `internal/config`, `internal/db`, `internal/crypto`, `internal/gitanalysis` | **Kept as scaffolding**, not because they're wanted features but because `report`/`calibration`/`store`/`embed` still need them to compile and run against Postgres in CI. `gitanalysis`'s own *domain* (blame-survival, SZZ, test-coupling) is separately ported to `gitstate-git` — the Go package survives only as `store/gitanalysis.go`'s persistence dependency. |
| `internal/store` (whole package) | **Kept, mixed.** Contains the not-yet-ported files above interleaved file-by-file with already-ported ones (`contribution.go`, `metrics.go`, `commits.go`, …) and SaaS-only ones (`billing.go`, `admin.go`, `capacity.go`, `calendar.go`, `leave.go`, …). Not split apart in this pass: it is one Go package, and file-level surgery here risks breaking the still-needed `report`/`calibration` build for no product benefit — the whole package is superseded by `crates/gitstate-store` regardless. |
| `cmd/migrate` | **Kept.** Still required to provision the Postgres schema the surviving Go reference (`store`/`report`/`calibration`) builds and tests against. |

## Migration collision guard

There are two migration sets now, and they must not fight:

- **`migrations/`** (repo root) — the legacy Go Postgres migrations. Kept verbatim; the Rust code
  never reads them.
- **`crates/gitstate-store/migrations/`** — the SQLite migrations for the local app. This is the
  **only** place the Rust store looks. Keeping them inside the crate is a deliberate rule (T11) so a
  forward-only Rust migration is never confused with a Postgres one.

## Data migration for existing SaaS users

There is intentionally **no** automated import from a hosted gitstate instance into the local app:
the SaaS stored multi-tenant, per-org data behind auth, while the local app derives everything from
*your* git and forge on *your* machine. To reproduce your view locally, add your repos
(`gitstate repo add …`) and scan — the state, contributions, and classifications are re-derived from
the real ledger (git), which is exactly the point.

## Tracking the port

Progress is tracked phase-by-phase in [ROADMAP.md](../ROADMAP.md) (Phase 4 — *Staged port of the
legacy Go domains*) and live in [PROGRESS.md](../PROGRESS.md). Each ported domain gets a parity check
against the Go reference before the Go source is retired.
