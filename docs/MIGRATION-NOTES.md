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
| `internal/calibration` (cohort/curve empirical-Bayes effort calibration) | **Kept — NOT YET PORTED.** No `calibrat`/`cohort`/`bayes`/`shrinkage` logic exists in `crates/`. |
| `internal/embed` (local semantic embedder) + `store/search.go` + `store/embeddings.go` | **Kept — NOT YET PORTED.** FTS+fuzzy search "by meaning, not exact keywords" and its embedding backing store; no cosine/embedding/vector code exists in `crates/`. |
| `store/agent_runs.go` + `cmd/gittrack`'s `log-run`/`runs`/`whoami` + `cmd/gitstate-mcp` | **Kept — NOT YET PORTED.** The agent-native write path (an AI agent records what it did) and the MCP bridge that exposes gitstate to Claude Code/Cursor-class hosts. `gitstate-cli` has no MCP server and no run-logging command today. |
| `store/context_bundle.go` + `cmd/gittrack`'s `context <issue>` | **Kept — NOT YET PORTED.** Token-efficient issue+PR context assembly for an agent starting work; distinct from `gitstate-cli context` (which is the CRDT-synced *saved working set* feature, T8) — same word, different feature. |
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
