# gitstate — Architecture &amp; Product Decisions

Format: **Decision → Why → Consequence**. When a choice isn't covered here, pick the option that best
serves *derived-not-entered*, *measure-work-not-workers*, and *evidence-with-visible-gaps* — and,
since the transform, *local-first over hosted* — then append a new entry.

The transform decisions (T-series) supersede the earlier SaaS architecture decisions (A-series,
retained below for provenance). Where a T-entry and an A-entry conflict, the T-entry wins.

---

## Transform decisions (local-first, P2P) — current

**T1. Standalone local-first desktop app, not a SaaS.** gitstate is delivered as a Rust core over
plain SQLite, wrapped in a Tauri desktop app, plus a headless daemon that is the always-on peer. →
No multi-tenant server, no Postgres, no billing cloud, no account. → The product essence (derive
state/effort/contribution/classification from git + forge) is unchanged; only *where it runs* flips —
onto the user's machine.

**T2. One daemon serves both desktop and headless.** `gitstate-daemon` (axum) serves `web/dist` as an
SPA *and* the JSON API. The Tauri shell boots this same daemon on an ephemeral local port; the React
UI points at it. → The UI is never forked into the desktop app; there is exactly one API surface. →
`gitstate serve` (headless peer) and the desktop app are the same core with different front doors.

**T3. Forge access is local, using the user's own credentials.** GitHub/GitLab are read by shelling
the user's `gh`/`glab` CLI (REST/GraphQL only as a token fallback when the CLI is absent). → No
gitstate-hosted forge broker, no stored OAuth apps, no tenant tokens. → A plain scan of a local repo
makes zero network calls; forge scans use only the credentials already on the box.

**T4. Classification is local-only.** Work-item classification and effort judging run against the
user's LLM endpoint (llmux / any OpenAI-compatible URL via env) or a deterministic heuristic when
none is configured. → Better privacy, no cloud dependency, works offline. → Corrections train a
**local personalization** store (T6); nothing about the user's work is pooled.

**T5. Label alignment is a signed data file, not a service.** So peers agree on what a category
*means*, gitstate ships a versioned, content-addressed, **ed25519-signed taxonomy** as embedded data,
overridable via `GITSTATE_TAXONOMY_PATH`. `verify()` recomputes the content hash, checks the pinned
public key, and verifies the signature — **fail-closed**: a bad signature falls back to local-only
categories and never silently trusts. → Cross-peer agreement without a running registry. → The dev
key ships in-repo (noted below); production re-signs with the release key.

**T6. Personalization replaces pooled fine-tuning.** Each box learns its own conventions from the
user's classification feedback and re-ranks future suggestions locally. → No feedback ever leaves the
machine; there is no shared model to poison or leak into. → `record_feedback` + a local prior; no
network path exists.

**T7. Only "needs a view of strangers you'll never meet" belongs to a coordinator.** Cross-population
features — trending, "similar repos", "others tagged this" — are the *only* things that require
seeing beyond your own peers, so they are **not built**; a dormant optional coordinator seam is left
and nothing more. → No anti-spam/sybil tier (a tax on the unbuilt discovery layer) and no pooled
feedback. → Everything a git tool is actually for stays local + P2P.

**T8. Contexts and categories sync peer-to-peer as CRDTs.** The sharable unit is a **context** (a
saved working set: repos, PR refs, notes, tags); categories are shared too. Both are CRDTs (LWW
scalars + OR-Sets over a hybrid logical clock), merged over the shared vulos/DMTAP sync substrate —
never a bespoke stack, never a central hub. → Two peers converge with no authority in the middle;
derived caches (commits, contributions, project state) are *local* and never synced. → Local edits
and remote merges share one op-application path.

**T9. Replication is compiled in; reaching a peer is what is opt-in.** `gitstate-sync` was once
excluded from the workspace behind a `sync-dmtap` feature, because it carried an optional **git**
dependency on the `envoir` repository and Cargo resolves optional sources during workspace
resolution — so a plain build shelled out to that remote even with the feature off. Two things were
wrong with that. The dependency was a *product* importing a *product* to get a merge engine; and the
feature wired nothing — both transports behind it returned success and an empty list, so every
document that mentioned it described a mechanism that did not exist. The exclusion also kept
`cargo test --workspace` from ever compiling the crate's tests.

→ The engine is now the published substrate crate (`kotva-sync`, crates.io) and a **dev** dependency,
used to hold gitstate's own algebra against it; the feature and the stub transports are gone;
`gitstate-sync` is a normal member. `Cargo.lock` has no git sources. → Sync is opt-in at *enrolment*,
not at build time: no feature to compile, and no peer until an operator types a URL and a key.

**T10. Relicense to MIT OR Apache-2.0; drop the EE tier.** The suite standard is MIT OR Apache-2.0
(every sibling — slipscan, diwan, wede — matches). With no multi-tenant service, the open-core AGPL +
commercial `ee/` split is obsolete. → Root carries `LICENSE-MIT` + `LICENSE-APACHE`; the AGPL
`LICENSE` and `ee/` are removed (history preserved). → No build tags, no runtime license check.

**T11. The legacy Go server stays in-tree for a staged port.** `internal/`, `cmd/`, `migrations/`,
`go.mod`, `go.sum` are kept **byte-for-byte** as the reference we port from — DORA metrics,
effort/estimation, involvement, evidence-invoice (reframed as an optional *local* report), NL→report.
→ Nothing is lost in the pivot; each domain is ported to Rust and only then is its Go source removed.
→ The Rust SQLite migrations live inside `crates/gitstate-store/migrations/`, never at repo root, to
avoid colliding with the kept Go `migrations/`.

**T12. Billing is not rebuilt as a service.** The legacy evidence-invoice (git-backed lines, gaps
flagged for a human) was genuinely useful; the *collection* half (Paystack, USD→ZAR, multi-tenant
charging) was SaaS scaffolding. → If ported, invoicing returns only as an **optional, local** report
generator. → No payment provider, no exchange-rate service, no charging path.

> **Dev taxonomy key.** The taxonomy signature currently uses a **development** ed25519 keypair
> generated during the transform; the public key is pinned as `DEFAULT_TAXONOMY_PUBKEY` in
> `gitstate-core` and the embedded `default_taxonomy.json` is signed with the matching private key.
> This proves the fail-closed verify path end-to-end. **Production must re-sign the default taxonomy
> with the offline release key** and update the pinned constant before any signed distribution.

> **T11 resolution (2026-08-04).** Every `internal/` package and `cmd/` binary was read against its
> Rust counterpart (or lack of one) and classified PORTED / SAAS-ONLY-DROP / NOT-YET-PORTED — full
> evidence in the domain-map commit and in [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md). The
> port is **not complete**; this is a partial removal, not the "only then is its Go source removed"
> closure T11 describes for the whole tree.
>
> Removed (`git rm`, history preserved): the domains with a verified Rust equivalent —
> `internal/git`, `internal/sync`, `internal/contribution`, `internal/contributors`,
> `internal/metrics`, `internal/importer`, `cmd/seed`, `cmd/seedgit` — and everything that only ever
> served the abandoned multi-tenant SaaS server — `cmd/gitstate`, `internal/api`, `internal/admin`,
> `internal/auth`, `internal/middleware`, `internal/oauth`, `internal/calendar`, `internal/capacity`
> (package), `internal/chat`, `internal/notifications`, `internal/email`, `internal/webhooks`,
> `internal/githubapp`, `internal/web`, `internal/docs`, `internal/jobs`, `internal/analytics`.
>
> **Not removed — genuinely not yet ported, real functionality that would be lost:**
> `internal/report` (NL→report + dashboard burndown/throughput; no Rust equivalent at all),
> `internal/calibration` (empirical-Bayes effort-calibration curves; no Rust equivalent),
> `internal/embed` + `store/search.go` + `store/embeddings.go` (local semantic + fuzzy search over
> issues; no Rust equivalent), `store/agent_runs.go` + `cmd/gitstate-mcp` + `cmd/gittrack`'s
> `log-run`/`runs`/`whoami`/`context` (the agent-native write path and the MCP bridge — `gitstate-cli`
> has neither today), and `store/context_bundle.go` (token-efficient issue+PR context for an agent,
> distinct from `gitstate-cli context`'s CRDT saved-working-sets).
>
> **Kept only as scaffolding** (not wanted features in themselves, but load-bearing for the packages
> above to keep building and testing against Postgres in CI): `internal/config`, `internal/db`,
> `internal/crypto`, `internal/llm` (its diff-difficulty domain IS ported to `gitstate-classify`; the
> Go package stays because `internal/report` still imports it for status synthesis), and
> `internal/gitanalysis` (its domain IS ported to `gitstate-git`; the Go package stays only because
> `internal/store`'s persistence layer imports its types). `internal/store` itself stays whole — one
> Go package interleaving not-yet-ported files with already-ported and SaaS-only ones — rather than
> risk file-level surgery breaking the still-needed `report`/`calibration` build. `migrations/` and
> `cmd/migrate` stay to keep provisioning that Postgres schema. `go.mod`/`go.sum` are **not** removed:
> not all Go fell into PORTED/SAAS-ONLY, so the T11 exit condition ("only then is its Go source
> removed") has not been reached for the tree as a whole. `scripts/go-gate.sh`'s coverage floors were
> re-measured against the smaller tree (12 packages / 105 files / 10 tested, was 37/243/29) so the
> `go:` CI job keeps meaning something instead of asserting stale numbers.
>
> **Port plan (2026-08-05, branch `port-plan`, plan-only — no code moved).** The remaining five
> domains (report, calibration, search/embeddings, agent_runs/MCP, context_bundle) all fit the
> single-file SQLite model with no architectural gap; see [docs/PORT-PLAN.md](docs/PORT-PLAN.md) for
> the full per-domain table, dependency graph, drop candidates (`store/planning.go` — zero live
> callers; `internal/crypto` — no path to any real domain, both missed by the file-level granularity
> of the 2026-08-04 sweep), third-party-dependency check (none need a new crate — even the LLM client
> and the embedder already have a Rust home or a dependency-free path), and the recommended six-wave
> order (agent_runs/MCP → calibration → context_bundle → search/embeddings → report/NL→report →
> final `internal`/`migrations`/`go.mod` cleanup).
>
> **Wave 1 shipped (2026-08-05, branch `port-agent-runs`): `store/agent_runs.go` + `cmd/gitstate-mcp`
> + `cmd/gittrack`'s `log-run`/`runs`/`whoami`.** `org_id`/RLS dropped — one tenant, nothing left to
> scope (see the new `agent_runs` table's migration comment for the full reasoning). `gittrack` and
> `gitstate-mcp` are folded into `gitstate-cli` as planned, not shipped as new standalone binaries:
> `gitstate agent log-run`/`runs`/`whoami` and `gitstate mcp`. Both call `gitstate_daemon::ops`
> **in-process** against the local SQLite file — the same pattern every other `gitstate` subcommand
> already uses — rather than over HTTP with a token, which is *also* the resolution to the wave's
> flagged MCP auth-scope spike: there is no second tenant for a token to distinguish, and gating one
> local subprocess while every sibling subcommand stays open would be theater. The daemon still grew
> `/api/agent-runs` (create + list) for parity with every other domain's dual CLI+HTTP exposure (the
> web dashboard has no agent-runs screen yet, but the route exists and is gated by the same
> `AdminAuth` posture as `/api/repos` — no new scope). `gittrack`'s `context`/`pr`/`issues`
> subcommands are **not** included — those read the `context_bundle` domain, a separate wave, still
> not yet ported despite being listed alongside `whoami` in `gittrack`'s own six-subcommand binary.
> Go's `internal/store/agent_runs.go` stays in-tree unchanged; nothing is deleted until the final
> cleanup wave. `cargo test --workspace` 218 → 227 (+9: 3 in `gitstate-store`, 5 in `gitstate-daemon`,
> 1 in `gitstate-cli`).
>
> **Wave 2 shipped (2026-08-05, branch `port-calibration`): `internal/calibration` +
> `store/calibration.go`.** New `gitstate-calibrate` crate, mirroring the Go package's own
> `cohort.go`/`curve.go`/`recompute.go` split file-for-file: `cohort::size_bucket`/`change_type`/
> `cohort_candidates`/`top_dirs_from_paths` are a near-line-for-line port with Go's own test tables
> reproduced as Rust assertions (not re-derived from scratch), and `curve`'s empirical-Bayes math
> (`default_secs_for_difficulty`, `difficulty_bucket`, `recency_weight`, `weighted_quantiles`,
> `shrink_to_prior`, `calibrated_secs`) does the same. One deliberate divergence, forced by wave 0's
> schema rather than invented here: Go's `BackfillActualSecs` reads a persisted Postgres `cycle_times`
> table that Rust never built (`docs/PORT-PLAN.md` §1 already decided `analytics::cycle_times`
> supersedes it), so `Store::backfill_actual_secs` computes lead time directly from
> `work_items.merged_at - created_at` in whole seconds — deliberately *not* routed through
> `analytics::cycle_times`'s `f64`-hours return, to avoid a needless float round-trip on a value this
> store persists as an integer. Persistence: migration `0004_calibration.sql` adds
> `effort_calibration` + `effort_accuracy` (both `org_id`-free, same reasoning as wave 1's
> `agent_runs`) and five nullable columns on `effort` (`predicted_secs`, `actual_secs`, `cohort_key`,
> `size_bucket`, `change_type`) via `ALTER TABLE`, following 0002's precedent — the migration's own
> comment flags a latent (not active) hazard: `save_effort`'s `INSERT OR REPLACE` would reset these
> columns on a re-judge, since Rust's `effort` table is one row per work item where Go's
> `effort_estimates` is one row per estimate-in-time; left for whichever wave wires a live writer.
> **No daemon route or CLI surface**, deliberately: `RecomputeCalibration`/`CalibratedSecs`/
> `ListExemplars` have no live caller in the Go reference either — their only intended caller was the
> multi-tenant HTTP server the 2026-08-04 sweep already removed as SaaS-only — so adding an `/api/*`
> route here would be surface for its own sake. Ported as a library instead, for wave 3
> (`context_bundle`) to call for a real `EstimateBrief`. Numerical fidelity was checked two ways: (1)
> every Go unit-test fixture in `cohort_test.go`/`curve_test.go` reproduced verbatim as a Rust
> assertion (12 tests), and (2) a full `recompute_calibration` pass against a real `SqliteStore` seeded
> with hand-picked merged-PR timings, asserting the exact resulting median/p25/p75/mean/MAE/bias
> numbers by hand computation, not just "it ran" (2 integration tests) — plus 4 new `Store`-level
> roundtrip tests exercising the new SQL directly (the dynamic `IN (...)` clause, the `ON CONFLICT`
> upserts, backfill idempotency, exemplar ordering). `internal/calibration` and `store/calibration.go`
> stay in-tree unchanged; nothing is deleted until the final cleanup wave. `cargo test --workspace`
> 227 → 245 (+18: 14 in the new `gitstate-calibrate` crate, 4 in `gitstate-store`).
>
> **Wave 3 shipped (2026-08-05, branch `port-context-bundle`): `store/context_bundle.go` +
> `cmd/gittrack`'s `context <issue>`/`pr <id>`.** Bundle-assembly types land in
> `gitstate-core::domain` (`IssueContextBundle`, `IssueSummary`, `PrBrief`, `CommitBrief`,
> `SimilarIssue`, `EstimateBrief`, `PrContextBundle`, `PrDetail`, `PrChangeShape`, plus `EffortRow` —
> a read-only view of `effort`'s base columns AND wave 2's five calibration columns together, kept
> separate from `EffortEstimate` so no existing `save_effort`/`list_effort` caller is touched). Two
> new read-only `Store` methods: `get_work_item(id)` (a bare-id lookup `list_work_items(repo)` can't
> do) and `get_effort(item)`. The assembly itself is `crates/gitstate-daemon/src/ops.rs`'s new
> `build_issue_context`/`build_pr_context`, mirroring Go's `BuildIssueContext`/`BuildPRContext`
> function-for-function (`relatedPRs`, `recentCommits`, `similarIssues`, the trim/cap constants) —
> called by `gitstate-cli`'s new `agent context <issue-id>`/`agent pr <id>` subcommands and by two new
> MCP tools, `get_issue`/`get_pr_context` (`crates/gitstate-cli/src/cmd/mcp.rs`), both in-process, no
> HTTP, matching wave 1's established pattern. `get_issue`/`get_pr_context` are exactly the two MCP
> tools wave 1 left unwired ("Go's other five tools ... depend on domains this wave explicitly
> excludes ... context_bundle = a later wave") — the daemon now has 3 of 6 Go MCP tools ported
> (`log_agent_run`, `get_issue`, `get_pr_context`); `search_issues` (search/embeddings, a later wave)
> and `list_issues`/`update_issue_state` (plain work-item listing/mutation, a different domain) remain
> unstubbed on purpose. **No daemon HTTP route**: unlike wave 1's `/api/agent-runs` (added for parity
> even without a web screen), this wave declines one with the same discipline wave 2 used for
> calibration — nothing consumes it. The CLI and MCP both call `ops` in-process; no web UI reads a
> bundle; and Go's own HTTP handler for this domain, `internal/api`, was already `git rm`'d as
> SaaS-only in the 2026-08-04 sweep, so there is not even a Go reference implementation left calling
> this over HTTP to point at. Two deliberate shape departures, both forced by earlier waves' schema
> choices, not invented here: `codeAreas` no longer reads a `task_files` table (none exists in Rust,
> and the table backed a planning feature with zero live callers even in Go, `docs/PORT-PLAN.md` §3)
> — it is re-derived from `WorkItem.files_touched` via `gitstate_calibrate::cohort::top_dirs_from_paths`,
> reusing wave 2's own helper rather than inventing a second one. `IssueSummary` drops `assigneeId`:
> `WorkItem` has no assignee field (only `author_login`, a different person in general), so the field
> is dropped rather than faked. **The one genuine behaviour change, made deliberately and flagged
> here rather than smuggled in:** Go's `predicted_secs` column has no writer in EITHER language today
> (`internal/store/estimates.go`'s own comment says it is "populated by `EstimateForPR`", but that
> function does not exist anywhere in the Go tree — confirmed by grep), so a byte-faithful port would
> forever read `NULL` and defeat the entire reason wave 2 was sequenced before this one. Instead,
> `build_estimate_brief` calls `gitstate_calibrate::curve::calibrated_secs` LIVE whenever a persisted
> value is absent (true for every row today), yielding a real calibrated number — falling back to the
> cold-start fixed prior with zero calibration history, never a placeholder or null. `size_bucket`/
> `change_type` are computed the same way (live, via `gitstate_calibrate::cohort`) when not already
> persisted, using `WorkItem.files_touched`/`title`/`repo_id` as the diff-shape input — churn
> (additions/deletions) is always 0, the same documented degradation `ops::effort_items` already
> lives with, since `WorkItem` never persisted per-item add/delete counts. Commit `isAgent` reuses
> `gitstate_git::util::detect_agent` (the same heuristic contributor-derivation already uses) rather
> than inventing a second one — `Commit` carries no `is_agent` column of its own. `store/issues.go`/
> `store/pull_requests.go` remain unported, per the plan: the bundle reads through `WorkItem`
> (`WorkKind::Issue`/`WorkKind::Pr`), not a replica of the Postgres-shaped structs. Tested with 14 new
> tests specifically exercising the token-budget/truncation logic (every cap — `related_prs` at 5,
> `recent_commits` at 8, `similar_issues` at 3, `code_areas` at 10, body at 800 chars, titles at 100 —
> seeded past its limit and asserted to land exactly at the limit, not over or silently under) plus
> the live-vs-persisted calibration read path, in `crates/gitstate-daemon/tests/context_bundle.rs`
> (11) and `crates/gitstate-store` (2: `get_work_item`, `get_effort`), plus 1 direct unit test for the
> new `gitstate_core::analytics::lead_time_secs` helper. `internal/store/context_bundle.go` and
> `cmd/gittrack` stay in-tree unchanged; nothing is deleted until the final cleanup wave. `cargo test
> --workspace` 245 → 259 (+14: 11 in `gitstate-daemon`, 2 in `gitstate-store`, 1 in `gitstate-core`).
>
> **Wave 4 shipped (2026-08-05, branch `port-search`): `internal/embed` + `store/search.go` +
> `store/embeddings.go`.** **The wave's flagged spike, resolved:** SQLite has **FTS5**, confirmed
> available with **zero `Cargo.toml` change** — the plan's suggestion of enabling an `fts5` `rusqlite`
> feature was wrong; no such feature exists on `rusqlite` 0.32 at all, and `libsqlite3-sys`'s
> `bundled` build (already on) passes `-DSQLITE_ENABLE_FTS5` unconditionally when it compiles SQLite
> from source. SQLite has **no `pg_trgm` and no trigram function of any kind**, and this build wires
> in neither `spellfix1` nor `editdist3` (separate, non-default `libsqlite3-sys` extensions this
> workspace does not enable — adding one would have been exactly the from-scratch-verification risk
> the plan flagged this domain for). The fuzzy fallback is therefore a **hand-rolled trigram-Jaccard
> function** (`gitstate_search::fuzzy::trigram_similarity`) — no new crate (`strsim` was the plan's
> other named option; declined, since Levenshtein/Jaro-Winkler are a different algorithm shape than
> trigram-set overlap, and the embedder already proves hand-rolled trigram logic is cheap and correct
> in this codebase). It replicates pg_trgm's own padding convention and exact Jaccard formula for the
> **symmetric** `similarity()` function pg_trgm exposes. **It does NOT implement `word_similarity()`**
> — Postgres's *asymmetric* "does some substring of the longer text closely match the shorter query"
> search, which Go used specifically for PR titles and commit messages (issues used `similarity`
> already, so that half ports with no behaviour change). **The real, stated ranking difference:** a
> long PR title or commit subject that only partially echoes the query will rank LOWER under this
> port than it did in Go, because whole-string Jaccard is diluted by the rest of a long text where
> Go's asymmetric search would have ignored the irrelevant remainder. The floor number (0.4) is kept
> unchanged even though the function it gates is not the same function, so the practical effect is
> "somewhat stricter for long targets, unchanged for short ones", not a re-tuned threshold.
> `internal/embed`'s claim of being dependency-free stdlib Go checked out exactly as the plan said: no
> new crate, `gitstate_search::embed` is FNV-1a + `std` math, near-line-for-line, with one deliberate
> hardening — a `BTreeMap` replaces Go's `map` for the term-frequency accumulator so bucket-collision
> summation order is deterministic by construction rather than incidentally-almost-always-deterministic
> the way Go's randomized map iteration is. Storage: SQLite has no `pgvector` column type, so an
> embedding is a BLOB of little-endian f32 bytes (`embed::to_bytes`/`from_bytes`), not Go's
> `::vector`-cast text literal — round-tripped losslessly, proven byte-for-byte in
> `gitstate-search`'s test suite, not just "close enough". Persistence: migration `0005_search.sql`
> adds an FTS5 virtual table (`search_fts`, covering `work_items`' issue/PR rows plus `commits`,
> rebuilt fresh on every `search_fts` call rather than kept in sync via triggers — `work_items`/
> `commits`' TEXT primary keys can't address FTS5's external-content rowid mode anyway, and a full
> rebuild costs low milliseconds at this app's local scale) and `work_item_embeddings` (issues only,
> matching Go's own scope), plus four new `Store` methods (`search_fts`, `list_issues_needing_embedding`,
> `set_work_item_embedding`, `list_issue_embeddings`) — `org_id` dropped, same reasoning as every
> prior wave. New `gitstate-search` crate mirrors Go's own module split (`embed`/`fuzzy` are pure math,
> `rrf` is the Reciprocal Rank Fusion of FTS + vector-KNN issue rankings ported from `fuseHybrid`,
> `search` is the orchestrator calling `Store` the same way `gitstate_calibrate::recompute` does).
> **Ranking correctness tested three ways, not just "it ran":** (1) `rrf::fuse_hybrid` unit-tested
> with fixed FTS/vector fixtures and a hand-computed expected fusion order (not re-derived); (2)
> `embed`'s near-duplicate/typo-robustness properties tested with fixed strings and known-correct
> orderings, mirroring Go's own `embed_test.go` cases; (3) an end-to-end suite against a real
> `SqliteStore` (`gitstate-search/tests/search_against_real_store.rs`) proving each of the three
> ranking paths — FTS, vector-KNN, fuzzy — finds a **known-correct top result**, including a typo
> query that FTS5 structurally cannot match (a different stemmed token) but the vector path still
> ranks correctly via trigram-robust embedding. **Surface**: `ops::search`/`ops::embed_pending`
> wrappers (mirroring every prior wave's `ops` shape); `embed_pending` is now also called
> automatically, non-fatally, at the end of `scan_repo` — mirroring Go's own post-sync
> `EmbedPendingIssues` hook, so semantic search actually has vectors once a user scans a real repo;
> a new `gitstate search <query> [--type ...] [--limit N]` CLI command; and the `search_issues` MCP
> tool (schema ported from `cmd/gitstate-mcp/tools.go` near-verbatim, including Go's own
> single-value, non-array `type` filter) — **the exact tool wave 3's doc named as blocked on "a later
> wave"**, now unblocked. MCP has **4 of Go's 6 tools ported** (`log_agent_run`, `get_issue`,
> `get_pr_context`, `search_issues`); only `list_issues`/`update_issue_state` (plain work-item
> listing/mutation, wave 5's report/NL→report territory) remain unstubbed. **No daemon HTTP route**:
> checked `web/` for a consumer (none — the one `type="search"` input in `People.tsx` is an unrelated
> client-side filter box), so this follows waves 2/3's evidence-based precedent rather than wave 1's
> parity-route default. `internal/embed`, `store/search.go`, `store/embeddings.go` stay in-tree
> unchanged; nothing is deleted until the final cleanup wave. `cargo test --workspace` 259 → 294
> (+35: 6 in `gitstate-store`, 26 in the new `gitstate-search` crate [21 unit + 5 real-store
> integration], 3 in `gitstate-daemon`).
>
> **Wave 5 shipped (2026-08-05, branch `port-report`): `internal/report` — the last of the five
> domains.** Verified before writing anything: the 2026-08-04 sweep's "no Rust equivalent exists
> anywhere" for `internal/report` was already corrected by `docs/PORT-PLAN.md`, and re-checked here by
> reading `gitstate_core::analytics` directly — it already computed throughput, per-PR cycle-time
> trend, and issue/PR state counts, all already served at `GET /api/analytics`. What was genuinely
> missing, and all that this wave added: **burndown**, a **recent-activity feed**, **LLM status
> synthesis**, and **NL→report**. `burndown`/`recent_activity` landed as two new pure functions in
> `gitstate_core::analytics`, beside `throughput` — not a new crate, since this is the same rollup
> math wave 1–4 already established the shape for, not a new domain algorithm. `burndown` improves on
> Go's own version rather than copying it: Go's Postgres schema had no issue-close timestamp, so
> `store.BurndownSeries`'s own comment calls its `updated_at`-proxy a best-effort stand-in for "a
> proper point-in-time snapshot, which would require a history table"; `WorkItem` already carries a
> real `closed_at` (wave 3 already relied on it), so this port's burndown is exact, not approximated.
> No new migration — both functions read already-persisted `work_items`/`commits`. Status synthesis
> reuses Go's own prompt near-verbatim via `gitstate_classify::LlmClassifier::chat`, promoted from
> `pub(crate)` to `pub` (`docs/PORT-PLAN.md` §5's recommendation, taken — no second HTTP client).
>
> **NL→report is the security-relevant redesign the plan flagged from the start, and it is not a
> port.** Go's `report.AnswerQuery` had an LLM write a raw PostgreSQL `SELECT`, then validated it with
> a regex/keyword blocklist plus a positive table allowlist (`validateSQL`) inside a `db.WithOrg`
> read-only transaction — a defence shaped entirely around a multi-tenant RLS threat model ("stop the
> LLM from reading another org's row") that gitstate, single-user and single-SQLite-file, no longer
> has. Porting `validateSQL` verbatim would carry forward a defence tuned for a threat that is gone
> while leaving the threat that replaced it — **the LLM emits text that gets executed as a query
> against your only database file** — defended by nothing more durable than "the regexes catch every
> dangerous keyword forever". The new `gitstate-report` crate's `nl` module does not rebuild that
> allowlist, narrower or otherwise: it eliminates SQL generation entirely. The LLM's job narrows to
> picking one of seven named `ReportIntent` variants (`state_counts`, `throughput`, `cycle_time`,
> `burndown`, `recent_activity`, `top_contributors`, `label_breakdown` — a smaller, more aggregate-only
> surface than Go's own allowlist, which also named `effort_estimates`/`agent_runs`/`involvement` and
> could `SELECT` free-text columns like `issues.title`/`commits.message` verbatim; this port
> deliberately does not expose either) and filling in a handful of bounded scalar parameters (`repo_id`,
> `days`, `weeks`, `limit`). `serde`'s internally-tagged, `#[serde(deny_unknown_fields)]` enum
> deserializer either fully resolves LLM output to one of seven statically-dispatched Rust function
> calls or produces nothing (`Err`) — there is no code path anywhere in the crate from LLM text to a
> SQL string, so a destructive statement is not a value the enum can hold, structurally rather than by
> policy. `parse_intent`'s refusal path is tested for malformed JSON, an unrecognized intent tag, a
> smuggled extra field (an attempted `"sql"` key), an out-of-bounds numeric parameter, and the model's
> own scripted "unanswerable" escape hatch — plus a containment test showing a hostile `repo_id`
> (`'; DROP TABLE work_items; --`) is just an id that matches no repo, never executed as anything.
> **Both guards were mutation-tested**: `#[serde(deny_unknown_fields)]` and the bounds-check call were
> each temporarily removed in turn and the corresponding refusal test confirmed to **fail** (the
> smuggled field / the oversized limit were silently accepted) before being restored — matching wave
> 1's admin-gate standard. `repo_id` is never interpolated into anything; it flows through `Store`'s
> existing parameterised `list_work_items(repo)` lookup exactly like every other domain's repo-scoped
> read, so an unmatched value degrades to an empty result, not an error and certainly not a query.
> **No daemon HTTP route**: grepped `web/src` for `burndown`/`synthesize`/`AnswerQuery`/`nl_report` and
> found only `/api/analytics`, already served and unchanged — CLI-only (`gitstate report
> burndown|activity|status|ask`), the same evidence-based call waves 2–4 made for their own domains.
> `internal/report` stays in-tree unchanged; nothing is deleted until the final cleanup wave (wave 6).
> `cargo test --workspace` 294 → 311 (+17: 6 in `gitstate-core::analytics`, 11 in the new
> `gitstate-report` crate). `go build ./...`, `go vet ./...`, `scripts/go-gate.sh` (12/105/106/46
> unchanged), and `web/`'s `eslint`/`check:lint-config`/`tsc --noEmit`/`npm run build` all re-verified
> clean, since none of `internal/`, `cmd/`, `migrations/`, `go.mod`, or `web/` were touched.
>
> **T11 closure (2026-08-05, branch `remove-all-go`): wave 6 of 6 — the Go tree is gone.** With all
> five domains ported (waves 1–5), this wave deleted `internal/`, `cmd/gittrack`, `cmd/gitstate-mcp`,
> `cmd/migrate`, root `migrations/` (1,643 lines SQL), `go.mod`, `go.sum`, `scripts/go-gate.sh`, and
> `scripts/provision-db.sh` (Postgres role/RLS provisioning found, during this wave, to have no
> remaining caller once the CI `go:` job was gone — not on the plan's original list, same class of
> dead Postgres tooling, deleted alongside it) — in dependency order, leaves inward, one commit per
> step, `go build ./...`/`go vet ./...` proving deadness at every step but the last: `cmd/gittrack` →
> `cmd/gitstate-mcp` → `internal/report` → `internal/calibration` → `internal/llm` → `internal/store`
> → `internal/embed` → `internal/gitanalysis` + `internal/crypto` → `internal/db` → `internal/config`
> → (final commit) `cmd/migrate`/`migrations/`/`go.mod`/`go.sum`/`go-gate.sh` + the CI `go:` job.
> **Both flagged traps were tested by deletion, not argued from the plan**: `store/planning.go` went
> with the rest of `internal/store` and `go build` stayed clean — it really had zero callers, as the
> plan said. `internal/crypto` was deleted only after its claimed-dead tenants (`internal/llm`,
> `internal/store`) were themselves gone, and only then did `grep`/`go build` confirm zero remaining
> importers — the plan's judgement held. **One thing turned out to be more dead than the plan scoped**:
> `internal/llm` was only flagged for its `catalog.go`/`gateway.go` reselling dead weight, but with
> `internal/report` gone the whole package (`complete.go`, `service.go`, `openai.go`, `provider.go`,
> `org.go` included) had zero importers, confirmed by deleting it whole in one commit with `go build`
> staying clean. Nothing was found to be NOT dead; no restoration was needed at any step.
> `scripts/go-gate.sh`'s coverage floors were ratcheted down at every intermediate step (12/10/105/46 →
> 1/0/1/0 by the last Go-only commit) so the gate stayed meaningful instead of asserting stale numbers,
> then deleted in the same commit as `go.mod` per the plan's instruction. Full before/after floor table
> in [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md)'s matching T11 closure entry. Final state:
> zero `.go` files anywhere in the repo outside a gitignored `web/node_modules` vendor file; `cargo
> build --workspace`/`cargo clippy --workspace --all-targets -- -D warnings`/`cargo fmt --all --check`
> all clean; `cargo test --workspace` unchanged at **311** (nothing deleted was load-bearing for a Rust
> test); `web/` untouched, its `eslint`/`check:lint-config`/`tsc --noEmit`/`npm run build` all still
> exit 0 and the `/api/*` contract is unchanged; the daemon (`gitstate serve`) still starts and answers
> `/health` and `/api/repos`. `.github/workflows/ci.yml`'s `go:` job removed; no Go reference remains
> anywhere under `.github/workflows/`. `README.md`, `CONTRIBUTING.md`, `Makefile`, `ROADMAP.md`,
> `PROGRESS.md`, `CHANGELOG.md`, `SECURITY.md`, `docs/ARCHITECTURE.md`, `docs/security.md`, and the
> `site/docs/` mirrors of architecture/roadmap/changelog were updated to stop describing an in-tree Go
> reference that no longer exists. T11 is now closed for the whole tree — every domain either shipped a
> Rust equivalent and passed parity (waves 1–5) or was confirmed to have zero remaining callers before
> deletion (this wave) — not the partial removal the 2026-08-04 resolution note described. gitstate is
> pure Rust + TypeScript.

---

## Product disciplines (unchanged by the transform)

**P1. Derived, not entered.** Dev work's source of truth is git — merged = done, PR open = in
progress. → We only claim "derived truth" where it's real; we never infer contribution from thin air.

**P2. Involvement, never a score.** Contribution is **texture across six dimensions** (shipped,
review, effort, quality, ownership, durability) — never a single rank, never a bonus formula. → Review
and ownership are counted so seniors/reviewers/maintainers aren't zeroed. The composite is displayed
as evidence-texture, never a leaderboard.

**P3. Estimates are evidence, not guesses.** Effort comes from an LLM reading the *shape* of the
change (difficulty 1–13), not lines or commits; a deterministic heuristic stands in when no LLM is
configured. → No story-point input field as the source of truth; every estimate links to its git
evidence.

**P4. Evidence with visible gaps.** What git can see is derived; what it can't (meetings, research) is
flagged for a human to fill, never auto-invented. → We under-count rather than fabricate.

**P5. Agent-native from day one.** Agent identities (Claude Code, Dependabot, …) are first-class:
every contribution carries an `agent_pct` and commits are split human/agent. → Survives the shift to
agent-written code; autonomous work is counted honestly, not hidden.

---

## Legacy SaaS architecture (A-series) — superseded, kept for provenance

These describe the pre-transform multi-tenant Go+Postgres stack still present in-tree under
`internal/`, `cmd/`, and `migrations/`. They are **superseded** by the T-series for the standalone
app; they remain accurate for the legacy code during the staged port.

- **A1. Go backend, single binary.** Strong concurrency for repo sync + LLM fan-out; web build
  embedded via `embed`. *(Superseded by T1/T2: the new core is Rust; the Go server is reference-only.)*
- **A2. Postgres (Neon) + RLS for tenancy.** `SET LOCAL app.current_org` inside each request tx.
  *(Superseded by T1: single-user local app has no tenancy; storage is SQLite.)*
- **A3. pgx + hand-written SQL.** Predictable, queryable, reviewable. *(Legacy only; Rust uses rusqlite.)*
- **A4. Forward-only migrations.** `YYYYMMDD_NNN_name.sql`, no up/down, checksums. *(The Rust store
  keeps forward-only migrations, but under `crates/gitstate-store/migrations/` — T11.)*
- **A5. JWT access + rotating refresh tokens.** *(Superseded by T1: no auth in a single-user local app.)*
- **A6. OAuth config-gated.** *(Superseded by T3: forge access uses the user's own `gh`/`glab`.)*
- **A7. Open core + EE (GitLab model), AGPL core.** *(Superseded by T10: MIT OR Apache-2.0, no EE.)*
- **A8. Bill USD, charge ZAR (Paystack).** *(Superseded by T12: no billing service.)*
- **A9. Server-rendered super-admin HTML.** *(Superseded by T1: no cross-org admin in a local app.)*
- **A10–A12. Shared root env / config file+env / fly.io deploy.** *(Superseded by T1: the app resolves
  a local data dir and needs no deploy target; the daemon binds `127.0.0.1` by default.)*
