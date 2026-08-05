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
| `internal/report` (NL→report, dashboard burndown/throughput synthesis) | **Ported (T11 wave 5, 2026-08-05 — the last of the five domains).** The 2026-08-04 sweep's "no Rust equivalent exists anywhere" was corrected by `docs/PORT-PLAN.md` before this wave started, and re-verified here by reading both sides first: `gitstate-core::analytics` already computed throughput, per-PR cycle-time trend, and issue/PR state counts, all already served at `GET /api/analytics`. The genuinely missing pieces were narrower than the file inventory suggested — **burndown** (a cumulative open-vs-closed issue count over time), a **recent-activity feed**, **LLM status synthesis**, and **NL→report** — and that is exactly what this wave added, nothing more. `burndown`/`recent_activity` landed as two new pure functions in `gitstate_core::analytics`, next to `throughput` (not a new crate — this is the same rollup math, not a new domain algorithm). `burndown` is a genuine improvement over Go's own version, not a copy: Go's Postgres schema had no issue-close timestamp, so `store.BurndownSeries`'s own doc comment calls out its `updated_at`-proxy as a best-effort stand-in for "a proper point-in-time snapshot, which would require a history table"; `WorkItem` already carries a real `closed_at`, so the Rust version is exact. No new migration — both functions read `work_items`/`commits`, already-persisted tables. LLM status synthesis reuses Go's own prompt (near-verbatim) via `gitstate_classify::LlmClassifier::chat`, promoted from `pub(crate)` to `pub` so a second HTTP client was not needed (`docs/PORT-PLAN.md` §5's recommendation, taken). **NL→report is a security-relevant redesign, not a port**, and is the one piece of this whole plan flagged that way from the start: Go's `report.AnswerQuery` had an LLM write a raw PostgreSQL `SELECT`, then validated it with a regex/keyword blocklist plus a positive table allowlist (`validateSQL`), enforced inside a `db.WithOrg` read-only transaction — a design built entirely around a multi-tenant RLS threat model gitstate no longer has. That allowlist was **not** ported. The new `gitstate-report` crate's `nl` module instead narrows the LLM's job from "write SQL" to "pick one of seven named `ReportIntent` variants (`state_counts`/`throughput`/`cycle_time`/`burndown`/`recent_activity`/`top_contributors`/`label_breakdown`) and fill in a handful of bounded scalar parameters (`repo_id`, `days`, `weeks`, `limit`)" — there is no code path from LLM output to a SQL string anywhere in the crate; `serde`'s internally-tagged, `deny_unknown_fields` enum deserializer either fully resolves a request to one of seven statically-dispatched Rust function calls or produces nothing (an `Err`). The new threat this addresses ("the LLM emits text that gets executed as a query against your only database file") is answered structurally — a destructive statement is not a value `ReportIntent` can hold — rather than by a blocklist that has to be re-audited forever. Every `parse_intent` refusal path is tested (malformed JSON, an unrecognized intent tag, a smuggled extra field, an out-of-bounds numeric parameter, the model's own "unanswerable" escape hatch), plus a containment test showing a hostile `repo_id` (`'; DROP TABLE work_items; --`) is just an id that matches no repo. Both `#[serde(deny_unknown_fields)]` and the bounds-check call were each temporarily removed and confirmed to make their respective test **fail** before being restored (see `gitstate-report/src/nl.rs`'s module doc and this row's git history for the commands). No daemon HTTP route: `web/src` has zero references to burndown/activity/status/ask (grepped for `burndown`, `synthesize`, `AnswerQuery`, `nl_report` — only `/api/analytics`, already served, already unchanged, comes up) — CLI-only (`gitstate report burndown\|activity\|status\|ask`), the same evidence-based call waves 2/3/4 made. `internal/report` stays in-tree (kept compiling) until the final cleanup wave. Tests: 294 → 311 (+17: 6 in `gitstate-core::analytics`, 11 in the new `gitstate-report` crate). |
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

## T11 closure (2026-08-05) — the final cleanup wave: the Go tree is gone

Wave 6 of 6. With all five remaining domains ported (waves 1–5, above), this wave deleted the Go tree
in dependency order — leaves inward — committing after every step and using `go build ./...`/
`go vet ./...` as live proof of deadness at each one (the compiler signal only exists while `go.mod` is
still present, so it was used for every step but the last).

**Deletion order, and what `go build` proved dead at each step:**

1. `cmd/gittrack` — no importer anywhere in the tree (it was already a standalone client with zero
   `internal/` imports); its `context`/`pr`/`log-run`/`runs`/`whoami` subcommands were folded into
   `gitstate-cli` in waves 1 and 3.
2. `cmd/gitstate-mcp` — same: zero `internal/` imports, superseded by `gitstate-cli mcp`.
3. `internal/report` — zero remaining importers now that wave 5 shipped `gitstate-report`.
4. `internal/calibration` — zero remaining importers now that wave 2 shipped `gitstate-calibrate`.
5. `internal/llm` — **the whole package**, not just the `catalog.go`/`gateway.go` reselling dead
   weight the 2026-08-04 sweep and `docs/PORT-PLAN.md` flagged. With `internal/report` gone (step 3),
   `grep` found zero remaining importers of `internal/llm` at all: `complete.go`, `service.go`,
   `openai.go`, `provider.go`, `org.go` were all dead too, confirmed by `go build` staying clean when
   the entire package was removed in one commit. The plan's prediction ("that import edge should be
   gone") held, and held for more of the package than the plan itself scoped.
6. `internal/store` (whole package, 18,774 lines / 62 files) — zero remaining importers outside the
   package itself. This is where `store/planning.go` and `internal/crypto`'s droppability were actually
   tested, not just argued:
   - **`store/planning.go`** — the plan named it a zero-live-caller drop candidate. Confirmed: it went
     with the rest of `internal/store` in one commit and `go build ./...` stayed clean. It really was
     dead, exactly as the plan said, and needed no separate handling.
   - **`internal/crypto`** — the plan flagged this explicitly as a *judgement*, not a proven zero: its
     importers (`llm/org.go`, `store/calendar.go`/`connections.go`/`repo_tokens.go`/`llm_settings.go`)
     were "already-classified SaaS-only files inside packages kept whole", a claim, not a measurement.
     Tested by deletion, per instruction: `internal/llm` was deleted first (step 5), `internal/store`
     second (step 6, this one), and only *then* was `internal/crypto` deleted (step 9, below) — by
     which point `grep` found zero importers and `go build`/`go vet` confirmed it. **The judgement was
     correct.**
7. `internal/embed` — zero remaining importers (only ever used by `internal/store`, gone in step 6).
8. `internal/gitanalysis` — zero remaining importers (only tenant was `store/gitanalysis.go`, gone).
9. `internal/crypto` — zero remaining importers, per the trap write-up in step 6 above.
10. `internal/db` — zero remaining importers (every package that used it — `llm`, `calibration`,
    `report`, `embed` — was already gone).
11. `internal/config` — zero remaining importers outside its own test files; `internal/` is now empty.
12. Final commit, all together (so the repo was never in a state referencing a toolchain it lacks):
    `cmd/migrate`, root `migrations/` (11 files, 1,643 lines SQL), `go.mod`, `go.sum`,
    `scripts/go-gate.sh`, and `scripts/provision-db.sh` (Postgres role/RLS provisioning for the now-gone
    `gitstate_test` database — not on the plan's original list, found during this wave to have no
    remaining caller once `go-gate.sh`'s `go:` CI job was removed, and deleted alongside it as the same
    class of dead Postgres tooling). The CI `go:` job was removed from `.github/workflows/ci.yml` in
    the same commit.

**Nothing was found to be NOT dead.** Every deletion step's `go build ./...`/`go vet ./...` stayed
clean; no package had to be restored.

**`go-gate.sh` floor ratchet** (`MIN_PKGS`/`MIN_TESTED_PKGS`/`MIN_GO_FILES`, plus
`EXPECTED_DB_SKIPPED_TESTS`), one commit per step, tracking the tree exactly:

| After deleting | Packages | Tested pkgs | .go files | DB-skips |
|---|---|---|---|---|
| (start, wave 5 baseline) | 12 | 10 | 105 | 46 |
| `cmd/gittrack` | 11 | 9 | 99 | 46 |
| `cmd/gitstate-mcp` | 10 | 8 | 94 | 46 |
| `internal/report` | 9 | 7 | 92 | 46 |
| `internal/calibration` | 8 | 6 | 86 | 44 |
| `internal/llm` | 7 | 5 | 76 | 44 |
| `internal/store` | 6 | 4 | 14 | 0 |
| `internal/embed` | 5 | 3 | 11 | 0 |
| `internal/gitanalysis` + `internal/crypto` | 3 | 1 | 6 | 0 |
| `internal/db` | 2 | 1 | 5 | 0 |
| `internal/config` (PKGS narrowed to `./cmd/...`, `internal/` now empty) | 1 | 0 | 1 | 0 |

`scripts/go-gate.sh` itself was deleted in the final combined commit (step 12), alongside `go.mod`, so
the gate never had to assert a floor of zero against a tree it no longer had a job watching.

**Result:** zero `.go` files anywhere in the repo (`find . -name '*.go' -not -path './target/*' -not
-path '*/node_modules/*'` → 0; the one hit inside `web/node_modules` is a vendored, gitignored
third-party sample file, not part of this repo). `cargo build --workspace`, `cargo clippy --workspace
--all-targets -- -D warnings`, `cargo fmt --all -- --check` all clean. `cargo test --workspace` — still
**311** (unchanged; nothing deleted this wave was load-bearing for a Rust test). `web/`'s
`eslint`/`check:lint-config`/`tsc --noEmit`/`npm run build` all still exit 0 — `web/` was not touched,
and the `/api/*` contract is unchanged. The daemon (`cargo run -p gitstate-cli -- serve`) still starts
and serves `/health` and `/api/repos`. Docs/CI/Makefile/README/CONTRIBUTING/ROADMAP/PROGRESS/
CHANGELOG/SECURITY were updated to stop describing a Go tree that no longer exists (see decisions.md's
matching T11 closure entry for the full list).

T11 is now closed for the whole tree, not just the partial removal the 2026-08-04 resolution described:
every domain either had a Rust equivalent shipped and parity-checked (waves 1–5) or was confirmed to
have zero remaining callers before deletion (this wave). gitstate is pure Rust + TypeScript.
