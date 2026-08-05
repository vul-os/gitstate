# Port plan — finishing T11 (the Go → Rust staged port)

**This document ports nothing.** It is the plan the 2026-08-04 domain-map removal
promised: exactly what remains, in what order, at what cost, so `go.mod` can
eventually go away. No `crates/`, `cmd/`, `internal/`, `web/`, `go.mod`, or CI file
is touched by this pass. See [decisions.md](../decisions.md) T11 and its
2026-08-04 resolution note, and [MIGRATION-NOTES.md](MIGRATION-NOTES.md), for what
already happened; this is what happens next.

Measured today: **28,466 lines across 105 Go files** under `internal/` + `cmd/`
(`find internal cmd -name '*.go' | xargs wc -l`), plus 1,643 lines of Postgres
migrations. `go build ./...`, `go vet ./...`, and `scripts/go-gate.sh` are clean at
its current floors (12 packages / 105 files / 10 tested / 46 DB-skipped);
`cargo build --workspace` and `cargo test --workspace` are clean (218 tests);
`web/` ESLint and `check:lint-config` both exit 0. All confirmed at the end of
this pass (see the last section) — nothing regressed because nothing changed.

---

## 1. The persistence question, answered first

**Every remaining domain fits the single-file SQLite model. None of them needs a
server, a second process, or multi-user semantics — the Go code's Postgres-ness
was almost entirely tenancy plumbing (`org_id`, RLS, `db.WithOrg`) wrapped around
logic that is naturally per-machine.** Concretely, domain by domain:

- **Report/dashboard** — burndown, throughput, recent-activity: pure rollups over
  `work_items`/`commits`, single-user, no different from the six-dimension
  contribution rollups Rust already computes. NL→report's only genuinely
  Postgres-shaped piece is the RLS-scoping half of its security model (see below)
  — the SQL-generation-and-validate half is dialect-portable to SQLite.
- **Calibration** — empirical-Bayes curves over one person's own merged-PR
  history. There was never a multi-tenant reason for this to be en Postgres; `n`
  is one developer's sample size either way.
- **Search/embeddings** — FTS + a 256-dim hashed local embedding over one user's
  issues. Deterministic, dependency-free, and already smaller than what a single
  Postgres `pgvector` column held for one org.
- **Agent runs / MCP** — a write-path table plus a stdio bridge process; both are
  inherently single-operator already (one MCP host, one token).
- **Context bundle** — read-only assembly over data already local.

**What Rust already has to build on:** `crates/gitstate-store` (`SqliteStore`) is
a `Mutex<rusqlite::Connection>` over one WAL-mode file, with forward-only
migrations embedded in `crates/gitstate-store/migrations/` and a synchronous
`Store` trait (`crates/gitstate-core/src/traits.rs`) that all derivation code
depends on instead of on rusqlite directly. Adding a domain means: a new
migration file, a handful of new `Store` trait methods (or a narrower
domain-specific trait — see §4), and `store_impl.rs` implementations. This is
the same shape as every domain already ported (`repos`, `commits`, `work_items`,
`contexts`, `categories`, …) — there is no new architectural seam to invent.

**`migrations/` (root, Postgres) and `cmd/migrate` stay exactly as they are until
the very last Go file is deleted**, because they provision the `gitstate_test` DB
that the *still-compiling* Go reference (`internal/store`, `report`,
`calibration`) needs in CI. They are not touched piecemeal; they go away in the
same commit as `go.mod`. The two migration sets already have a documented
non-collision rule (`crates/gitstate-store/migrations/` only; the Rust code never
reads root `migrations/`) — nothing new needed there.

**One real finding that changes the shape of the work:** MIGRATION-NOTES.md says
`internal/report`'s dashboard has "no Rust equivalent at all... burndown doesn't
appear in crates/ at all." That is true for *burndown specifically*, but it
undersells how much of the rest already exists. Reading
`crates/gitstate-core/src/analytics.rs` (already shipped, already tested, already
served at `GET /api/analytics` by `crates/gitstate-daemon/src/routes/repos.rs`):

- `pub fn throughput(...) -> Vec<ThroughputPoint>` — literally `store.WeeklyThroughput`'s
  job, already done.
- `Totals.open_issues/closed_issues/open_prs/merged_prs` and `work_states: Vec<Slice>`
  — literally `store.IssueStateRollup`'s job, already done.
- `cycle_time: Vec<CyclePoint>` (one point per merged PR) — literally
  `report.go`'s `CycleTrendPoint` series, already done, and it is *exactly* the
  per-PR actual-lead-time signal `internal/calibration`'s backfill needs — no new
  persisted `cycle_times` table is required in Rust; `analytics::cycle_times`
  computes it live from `work_items` on every call, which is enough for both
  the dashboard trend and calibration's recompute.

So the genuinely missing pieces of "report" are narrower than the file inventory
suggests: **burndown** (a cumulative open-vs-closed-over-time series — absent),
**a recent-activity feed** (a literal "last N events" list — absent, but trivial:
sort `list_all_work_items()` by `updated_at`), **LLM status synthesis** (a prose
paragraph — absent), and **NL→report** (SQL-generation-from-English — absent).
The rollup math for the rest of the dashboard does not need to be re-derived;
it needs a data-shape adapter and, for burndown, one new pure function next to
`throughput` in `analytics.rs`.

---

## 2. Per-domain table

Lines are **measured**, not estimated (`wc -l`, production + test file split
shown). "Difficulty" is about porting cost specifically — a pure/deterministic
Go file with a matching Rust type is Low; genuine re-architecture (a different
security model, a missing index type) is High.

| # | Domain | Go lines (prod / test) | Depends on | Target Rust crate | Persistence implications | Difficulty |
|---|---|---|---|---|---|---|
| 1a | `internal/report` — dashboard rollup | 492 / 209 | `internal/store` (`report.go`, `metrics.go`'s `ListCycleTimes`), `internal/llm` (status synthesis) | **New**: small `gitstate-report` crate, or a `report` module inside `gitstate-core`/`gitstate-daemon` | Burndown needs one new pure function (`analytics::burndown`) fed by existing `list_all_work_items()` — **no new table**. Recent-activity is a sort, not a table. | **Low** — most of the rollup math (throughput, state counts, cycle trend) already exists in `gitstate-core::analytics`; only burndown + activity feed are net-new, both pure functions over data already in the store. |
| 1b | `internal/report` — NL→report | (included above) | `internal/llm.Complete`, `internal/db` (RLS-scoped read-only tx) | Same crate as 1a, using `gitstate-classify`'s existing `LlmClassifier` chat primitive (see §5) | The Postgres version's security model is RLS-shaped (`SET LOCAL app.current_org`, positive table allowlist, 5s `statement_timeout`, read-only tx). In SQLite there is no RLS to lean on — the port must reimplement `validateSQL`'s allowlist/regex defence **and add** a SQLite-appropriate hard stop (e.g. open the query on `rusqlite::Connection::query_row` with a temp read-only connection, or use `PRAGMA query_only = ON`) since the Go version's *last* line of defence (Postgres `default_transaction_read_only`) has no exact SQLite analogue at the connection level used for writes elsewhere. | **Medium** — the validator logic ports almost verbatim (regexes, keyword checks); the "make it actually unable to write" guarantee needs a deliberate SQLite mechanism, not a copy-paste. |
| 2 | `internal/calibration` | cohort.go 313, curve.go 229, recompute.go 189 (test: 140+138+310=588) — **1,691 incl. `store/calibration.go`** | `store/calibration.go` (persistence: `effort_calibration`, `effort_accuracy` tables), `analytics::cycle_times` (already exists, gives actuals) | **New**: `gitstate-calibrate` crate (algorithm) + new tables/methods in `gitstate-store` | Two new tables (`effort_calibration`, `effort_accuracy`) plus 3-4 new columns on the existing `effort` table (`predicted_secs`, `actual_secs`, `cohort_key`, `size_bucket`, `change_type`) — same forward-only migration pattern already used for 0002. | **Low** — `cohort.go` and `curve.go` (542 of the 731 production lines) are 100% pure functions, zero I/O, already unit-tested in Go in a way that translates almost line-for-line (`SizeBucket`, `ChangeType`, `CohortCandidates`, `WeightedQuantiles`, `ShrinkToPrior`). Only `recompute.go`'s read/write orchestration needs new `Store` methods. |
| 3 | `internal/embed` + `store/search.go` + `store/embeddings.go` | embed 195/115, batch.go 117; search.go 441/197; embeddings.go 133/282 — **1,480 total** | none (self-contained; `internal/embed` has zero external deps beyond stdlib) | **New**: `gitstate-search` crate | `embed.go`'s hashing-trick embedder is **already dependency-free stdlib-only Go** (`hash/fnv`, `math`, `strings`) — it ports to Rust std almost mechanically, no ML crate needed. Storage: an embedding is a `Vec<f32>` (bincode or JSON in a BLOB/TEXT column on `work_items` or a new `embeddings` table) — no `pgvector`. Similarity search is brute-force cosine over however many issues one person's local repos produce (hundreds to low thousands) — no ANN/HNSW index needed at this scale, so the Postgres version's HNSW index has **no Rust equivalent needed**, not because it's missing but because it's the wrong tool for a single-file local corpus. | **Medium** — the algorithm ports easily (Low); the *search* half is a genuine re-architecture (Postgres `tsvector`/`ts_rank`/`pg_trgm` → SQLite FTS5 + an in-app fuzzy/trigram fallback), not a line-for-line port. Needs the `fts5` Cargo feature enabling on `rusqlite` (currently only `bundled` is on). |
| 4 | `store/agent_runs.go` + `cmd/gitstate-mcp` + `cmd/gittrack`'s `log-run`/`runs`/`whoami` | agent_runs.go 235/188; mcp 1,233 (incl. 443 test); gittrack (shared with #5, see below) | `internal/db` (org-scoped tx — the *only* multi-tenant-shaped piece; drops trivially to a plain table), gitstate-daemon's existing bearer-token admin gate | **New**: `agent_runs` table in `gitstate-store`; MCP bridge as new `gitstate-mcp` crate/binary; `log-run`/`runs`/`whoami` folded into `gitstate-cli` (see recommendation below) | One new table, org-free (`id, repo_id, goal, diff_summary(json), tests_passed, human_action, iterations, cost_usd, supervisor_id, pr_id, issue_id, agent_name, branch, created_at`). MCP needs new daemon routes (`/api/search`, `/api/agent-runs`, `/api/context/issue/:id`, `/api/context/pr/:id`) which do not exist yet in `gitstate-daemon/src/routes/`. | **Low-Medium** — the table and the MCP JSON-RPC bridge (hand-rolled stdio loop, no SDK dependency in Go either) are mechanical ports. The work is mostly *wiring* — new daemon routes plus an MCP client pointed at `:7473` with the daemon's existing bearer-auth (`state.admin_ok`) standing in for the old `GITSTATE_TOKEN`. |
| 5 | `store/context_bundle.go` + `cmd/gittrack`'s `context <issue>`/`pr <id>` | context_bundle.go 498/136; gittrack (shared, see below) | `store/issues.go`/`pull_requests.go` (**not** ported — see §4, these are superseded by `WorkItem`, not replicated), `store/calibration.go` (estimate brief) | Reuses `Store::list_work_items`/a new `get_work_item(id)`, `list_commits`, plus calibration's `effort` columns. **No `task_files` table exists in Rust and none is planned** (`codeAreas` was a "cheap proxy" over a table that itself belonged to a planning feature with zero live callers — see Drop candidates). | **Low** — this is bundle-assembly logic (recency ordering, label-intersection similarity, trims/caps), not new derivation; it's built entirely on data structures Rust already has (`WorkItem`, `Commit`, `EffortEstimate` — once #2 adds the calibrated fields). The `codeAreas` field should be dropped or re-derived from `files_touched` on `WorkItem` (data already present) rather than porting `task_files`. |
| 6 | Scaffolding: `internal/config`, `internal/db`, `internal/crypto`, `internal/llm`, `internal/gitanalysis`, `internal/store` (whole), `migrations/`, `cmd/migrate` | config 419/340; db 124; crypto 99/148; llm 1,635 (mixed); gitanalysis 879/475; store (whole) 18,774 | see §4 | **None.** This is not a port target — see below. | n/a | n/a — **not a porting task at all**, see §3/§4. |

**gittrack line split.** `cmd/gittrack` (1,514 lines across 6 files: `main.go`
133, `client.go` 145, `types.go` 100, `render.go` 193, `commands.go` 474,
`gittrack_test.go` 469) implements six subcommands in one small binary:
`context`, `pr`, `issues`, `whoami` (domain 5/4 boundary), and `log-run`/`runs`
(domain 4). It is not worth splitting file-by-file for this table — it is one
cohesive, already-small CLI that happens to serve two domains. Counted once,
attributed to whichever of #4/#5 is ported first (see §6 order).

---

## 3. Drop candidates — do not port

Dropping is a legitimate outcome, not a shortfall. Two categories: things the
2026-08-04 sweep already named (repeated here for completeness), and two things
I found that the sweep's package-level granularity missed.

**Already named (decisions.md/MIGRATION-NOTES.md), reaffirmed:**
- `internal/llm`'s multi-provider reselling catalog/gateway (`catalog.go`,
  `gateway.go`, billing markup) — dead weight bundled into a package kept only
  for `report`'s status-synthesis call. Confirmed: `report.go` calls only
  `s.llm.Complete` and `s.llm.SynthesizeStatus`; nothing in the report/
  calibration/embed path touches `catalog.go` or `gateway.go`.
- `internal/gitanalysis`'s own domain (blame-survival, SZZ) — already ported to
  `gitstate-git`; the Go package's only remaining consumer is `store/gitanalysis.go`.

**New findings from this pass — precise, not "the whole store package":**

1. **`store/planning.go` (198 lines) + its test (244 lines) — zero live callers,
   anywhere.** `grep -rn "WeeklyVelocity\|SizedBacklog" --include="*.go" internal cmd`
   (excluding the file's own test) returns nothing. This is capacity-planning
   velocity/backlog machinery — the same SaaS-only territory as
   `capacity.go`/`calendar.go`/`leave.go`, which decisions.md already dropped —
   but `planning.go` itself was never named in the 2026-08-04 table. It should be
   deleted alongside those, not kept as if it were load-bearing.
2. **`internal/crypto` (99 prod / 148 test lines) has no path to any of the five
   real domains.** Its only callers are `internal/llm/org.go` (per-org LLM API
   key at-rest encryption) and `store/connections.go`, `store/llm_settings.go`,
   `store/repo_tokens.go`, `store/calendar.go` — every one of which is either
   already-classified SaaS-only or, like `llm_settings.go`, exists to store
   *per-org* provider credentials, a concept with no counterpart in a
   single-user app whose LLM endpoint is one `VULOS_LLMUX_URL` env var
   (`gitstate-classify::LlmClassifier::from_env`). None of `report.go`,
   `calibration/*.go`, `embed/*.go`, `agent_runs.go`, or `context_bundle.go`
   import `internal/crypto` or `llm_settings`. **`internal/crypto` is drop, not
   scaffolding** — it was miscategorized in the 2026-08-04 sweep as
   load-bearing scaffolding; it is actually dead weight whose only tenants are
   already-dead packages, just not yet deleted because `internal/store` is one
   Go package.
3. **`internal/config` and `internal/db` are not "kept because report/calibration
   need them" in any sense that survives the port** — they are the Postgres
   connection/tenancy plumbing (`Config.Database`, `Config.Auth`, `WithOrg`
   RLS-scoping). Every one of report/calibration/embed/agent_runs/context_bundle's
   Rust equivalents gets its configuration from plain env vars (mirroring
   `LlmClassifier::from_env`) and its persistence from `SqliteStore`'s single
   `Mutex<Connection>` — no pool, no per-request tx scoping, no org context to
   thread through. These two packages are not "domains with no Rust
   equivalent"; they are infrastructure with **no domain at all**, and they
   disappear the moment their last Go caller (the five real domains) moves —
   they were never going to be ported, only deleted.
4. **`store/issues.go` (389) and `store/pull_requests.go` (274) do not need
   porting for context_bundle**, even though `context_bundle.go` calls
   `GetIssue`/`GetPR`. Rust's `WorkItem` already models both issues and PRs
   generically (`WorkKind::Issue`/`WorkKind::Pr`); the bundle-assembly port
   should read through `Store::list_work_items`/a new `get_work_item(id)`, not
   replicate the Postgres-shaped `Issue`/`PullRequest` structs. These two files
   are dead weight riding on the same "`internal/store` is one package" excuse
   as everything else — they were already superseded when `internal/contribution`
   was ported.

**Genuinely cannot work locally (server-side/multi-user by nature) — already
excised, reaffirmed here as correctly dropped, not staged:** the whole
billing/invoicing/COGS layer (T12), OAuth/JWT auth, org-scoped admin, calendar
two-way sync, capacity/leave approval, webhooks receiver, notifications
digest — all listed in MIGRATION-NOTES.md already. Nothing new to add to this
list; the remaining five domains were specifically the ones with *no*
multi-user assumption baked in, which is why they survived the first sweep.

---

## 4. Dependency graph among the remaining domains

```
                    ┌─────────────────────────┐
                    │ gitstate-core::analytics │  (EXISTS — throughput, cycle_time,
                    │  (already ported)         │   state counts, totals)
                    └───────────┬───────────────┘
                                │ feeds
                    ┌───────────▼───────────────┐
        ┌──────────►│  1. report (dashboard)     │◄────────┐
        │           │  + burndown (new fn)       │         │
        │           │  + NL→report (new)         │         │
        │           └───────────┬────────────────┘         │
        │                       │ uses calibrated estimate  │
        │           ┌───────────▼────────────────┐          │
        │           │  2. calibration             │──────────┘ (cycle_times
        │           │  (cohort/curve/recompute)   │            already live)
        │           └───────────┬────────────────┘
        │                       │ EffortEstimate.{predicted_secs,...}
        │           ┌───────────▼────────────────┐
        │           │  5. context_bundle          │  (issue/PR agent context)
        │           │  (needs calibration's brief) │
        │           └───────────┬────────────────┘
        │                       │ exposed over HTTP by
        │           ┌───────────▼────────────────┐
        └───────────┤  4. agent_runs + MCP bridge │  (also independent: agent_runs
                     │  (log-run write path)       │   table has NO dependency on
                     └──────────────────────────────┘   1/2/5 — can move first)

  3. search/embeddings — INDEPENDENT of all four above (zero shared tables,
     zero shared code). Can be ported any time, in parallel with the others.
```

**The real ordering constraint is narrow:** `context_bundle`'s `EstimateBrief`
(difficulty, predicted seconds, size bucket, change type) is calibration's
output, so **calibration should land before context_bundle** if the bundle is to
carry a real calibrated estimate rather than a placeholder. `report`'s dashboard
does not strictly need calibration (Postgres's `Dashboard()` reads
`cycle_times` directly, not through calibration), but `report`'s NL→report
allowlist documents `effort_estimates` and would be more useful with calibrated
columns present. Everything else — `agent_runs`, `search`/`embeddings` — has
**no dependency edge** on the other four and can be ported whenever convenient.

**Unpicking `internal/store`.** The prior sweep's decision not to split this
package file-by-file was correct for the removal pass (surgery risk for no
benefit while report/calibration still needed it to build). For *this* plan, the
unpicking is not "split the Go package" — it's "identify the ~2,721 lines across
the 10 files in the table above that are the actual domains, and recognize that
the other ~16,000 lines of `internal/store` need **zero Rust work**, because
`crates/gitstate-store` already supersedes them." The practical unpicking
sequence is:

1. Port each of the 10 domain-specific store files' *logic* (not their Postgres
   SQL) into `gitstate-store`/new crates, per the table above.
2. Once all five domains are ported and parity-tested, delete `internal/store`
   **whole** — every remaining file in it (the ~48 non-domain files: `billing.go`,
   `admin.go`, `contribution.go`, `metrics.go`, `commits.go`, `analytics.go`,
   `contributors.go`, `issues.go`, `pull_requests.go`, `planning.go`, …) is
   either already-ported-elsewhere or SaaS-only-dead; none of them need surgery
   because none of them need porting. This is why the package can stay whole
   until the end without cost: nothing in it is waiting on file-level extraction
   except the 10 files this plan tracks.
3. `internal/gitanalysis`, `internal/crypto`, `internal/config`, `internal/db`
   fall away in the same commit as `internal/store` (their only callers), and
   `internal/llm`'s catalog/gateway dead weight falls away when `report.go`
   (its only report-side consumer) moves. `internal/llm`'s core `Complete`/
   `SynthesizeStatus` moves to a thin wrapper on `gitstate-classify`'s existing
   `LlmClassifier` chat primitive (see §5).

---

## 5. Third-party Go dependencies with no obvious Rust equivalent

Checked every remaining domain's imports. The honest result: **there are none
that need a new crate.**

- **Embeddings** (`internal/embed`): zero third-party deps — FNV hashing +
  stdlib math. Ports to Rust `std` directly (`std::hash` or a small in-house
  FNV-1a, `f32` arithmetic). No `candle`/`ort`/ONNX/tokenizers needed, because
  the shipped default was never a neural model.
- **LLM client** (`internal/llm`'s `Complete`/`SynthesizeStatus`, and NL→report's
  question→SQL call): `crates/gitstate-classify/src/llm.rs` already has a
  private `LlmClassifier::chat(system, user) -> Result<String>` built on
  `reqwest` (already a workspace dependency) against any OpenAI-compatible
  `/chat/completions` endpoint. **Recommendation: promote `chat` to a small
  public, reusable primitive** (either `pub(crate)` → `pub` plus a thin public
  wrapper type, or extract a tiny `LlmClient` struct shared by
  `gitstate-classify` and the new report crate) rather than standing up a
  second HTTP LLM client. This removes what looked like the single biggest
  "no Rust equivalent" risk in the whole plan.
- **Full-text search** (`store/search.go`'s `websearch_to_tsquery`/`ts_rank`/
  `ts_headline`): Postgres built-ins, not a Go dependency — no crate needed
  either way, just `rusqlite`'s `fts5` feature (currently not enabled; a
  one-line `Cargo.toml` change, `features = ["bundled", "fts5"]`).
- **Fuzzy/trigram fallback** (`pg_trgm`'s `similarity`/`word_similarity`): a
  Postgres extension, not a Go dependency. **Open risk, flagged for a spike**:
  SQLite has no built-in trigram similarity function; the port needs either (a)
  a small hand-rolled trigram-Jaccard/Levenshtein function (cheap, no crate,
  matches the spirit of `internal/embed`'s "dependency-free" ethos), or (b) a
  crate such as `strsim` (Levenshtein/Jaro-Winkler, no unsafe, small). Either is
  low-risk, but which one to use is a genuine design decision, not something to
  guess at in this plan — **needs a spike** before domain 3's port wave starts.
- **`pgvector`'s HNSW KNN index**: as noted in §2, not needed at all at
  single-user local scale — brute-force cosine over an in-memory `Vec<(id,
  Vec<f32>)>` loaded from the embeddings column/table is fast enough for
  hundreds-to-low-thousands of issues. If a future user has an unusually large
  local issue corpus, revisit; not a blocker now.
- **MCP protocol** (`cmd/gitstate-mcp`): the Go server is a hand-rolled
  newline-delimited JSON-RPC 2.0 stdio loop, **not** built on an MCP SDK
  (confirmed: `cmd/gitstate-mcp` imports only `bufio`, `encoding/json`, `net/
  http`, `net/url`, stdlib). No dependency question at all — the Rust port is
  the same hand-rolled shape over `serde_json` + `std::io`, or the official
  `rmcp` (Rust MCP SDK) crate if the founder prefers a maintained protocol
  layer over the current from-scratch one. **This is a design choice, not a
  gap** — flagging it as a decision point for the dispatch, not a risk.

---

## 6. What must be true before `go.mod` can be deleted

- [ ] Domain 1 (report: dashboard + burndown + recent-activity + NL→report)
      ported and parity-checked against the Go reference.
- [ ] Domain 2 (calibration: cohort/curve/recompute + new `effort_calibration`/
      `effort_accuracy` tables) ported and parity-checked.
- [ ] Domain 3 (search + embeddings, including the fuzzy-fallback spike above)
      ported and parity-checked; `rusqlite`'s `fts5` feature enabled.
- [ ] Domain 4 (agent_runs table + MCP bridge + `gittrack`'s `log-run`/`runs`/
      `whoami`, or their fold into `gitstate-cli`) ported; new daemon routes
      (`/api/agent-runs`, `/api/search`) exist and are auth-gated the same way
      the existing admin routes are.
      **Decision needed**: does the daemon's existing bearer-token admin gate
      (`state.admin_ok`) fully substitute for the Go server's scoped API tokens
      (`write:agent_runs`, read-only tokens), or does MCP need a narrower
      token/scope of its own even in a single-user app? Recommend: reuse the
      existing admin token — there is no second user to scope against — but
      confirm before wiring it, since it's a real (if small) trust-boundary
      choice.
- [ ] Domain 5 (context_bundle: issue/PR agent context) ported, reusing
      calibration's `EstimateBrief` equivalent; `codeAreas`/`task_files`
      dropped (see §3) or re-derived from `WorkItem.files_touched`.
- [ ] `gittrack` and `gitstate-mcp` retired as standalone Go binaries; their
      functionality either becomes new commands on `gitstate-cli` (recommended
      — avoids a second auth story and a second binary to release/sign) under a
      new `gitstate agent …` subcommand group (`gitstate context` is already
      taken by the CRDT saved-working-set feature — **do not reuse that name**),
      or ships as small standalone Rust binaries mirroring the Go structure.
      This is a naming/packaging decision for the dispatch, not a technical
      blocker either way.
- [ ] `internal/store`, `internal/gitanalysis`, `internal/crypto`,
      `internal/config`, `internal/db`, `internal/llm` deleted (per §4's
      unpicking sequence — no file-level surgery needed, delete whole once
      their last caller among the five domains has moved).
- [ ] `migrations/` (root, Postgres) and `cmd/migrate` deleted in the same
      commit as `go.mod`/`go.sum` (they exist only to provision the DB the
      dying Go reference tests against).
- [ ] `scripts/go-gate.sh` deleted (or its CI job removed) in the same commit;
      it has no subject left to gate.
- [ ] `.github/workflows/ci.yml`'s `go:` job removed.
- [ ] `docs/MIGRATION-NOTES.md` and `ROADMAP.md` Phase 4 updated to reflect
      completion (out of scope for this plan-only pass, but worth noting:
      `ROADMAP.md`'s Phase 4 checklist currently only lists "Reporting /
      NL→report" as outstanding — it predates this plan's finer breakdown and
      should be refreshed once porting starts, not as part of this pass).
- [ ] `go build ./...`, `go vet ./...` no longer apply (files gone); final
      `cargo build --workspace` / `cargo test --workspace` green with new tests
      for all five domains added.

---

## 7. Recommended port order, and why

**1. `agent_runs` + MCP bridge (domain 4) first.** Zero dependency on anything
else in this plan (no calibration, no report, no search). It is also the
highest leverage-per-line item: it is the one piece of "no Rust equivalent"
functionality that changes how *this very porting programme* can be operated —
once it exists, an agent working the later waves can log its own runs through
the tool it just built. Low-medium difficulty, self-contained, ships value
immediately.

**2. Calibration (domain 2) second.** Also has no dependency on the other three,
is the highest-ratio pure-function/total-lines domain (542 of 731 production
lines are zero-I/O math), and unlocks a *real* `EstimateBrief` for
context_bundle rather than a stub. Doing it before context_bundle avoids
building context_bundle twice.

**3. Context bundle (domain 5) third.** Depends on calibration (#2) for a
non-placeholder estimate; otherwise self-contained bundle-assembly logic over
data structures (`WorkItem`, `Commit`) that already exist. Finishes the
"agent-native" trio (4, 2, 5) as one coherent wave of work, since MCP's
`get_issue`/`get_pr_context` tools are exactly context_bundle's HTTP surface —
shipping MCP (domain 4) without context_bundle would leave two of six MCP tools
returning "not implemented."

**4. Search + embeddings (domain 3) fourth.** Fully independent of 1/2/4/5 (could
technically run in parallel with any of them), but ordered after because it
carries the plan's one open spike (fuzzy-fallback mechanism) and the one Cargo
feature change (`fts5`) — cleaner to resolve that spike with nothing else
mid-flight, and it is the biggest re-architecture-vs-line-count domain (Medium,
not Low, unlike 2/4/5).

**5. Report/dashboard + NL→report (domain 1) last.** Two reasons. First, it is
the domain where "port" most means "wire existing Rust analytics into a new
surface" rather than translate Go — doing it last means burndown/recent-activity
can lean on calibration's and search's data being present too (a synthesized
status paragraph that can mention calibrated estimates and searchable context is
a better feature than one that can't). Second, NL→report is the one piece of
this whole plan that is a **security-relevant redesign, not a port** (§2, 1b) —
it deserves to be the last, most-reviewed, least-rushed piece, sequenced after
the team has done four smaller domains and knows the new schema (`effort`
columns, `agent_runs`, embeddings) NL→report's allowlist will need to document.

This order also front-loads the **lowest-difficulty, highest-confidence** work
(4, 2, 5 are Low/Low-Medium) and back-loads the **two Medium-difficulty
re-architectures** (3's search-index swap, 1b's security-model swap) — leaves
first, in the sense the prior domain-map pass used it: do the parts where the
Rust shape is obvious before the parts where it has to be invented.

---

## 8. Effort estimate

| Wave | Domain(s) | Real production lines to port (excl. test, excl. dead scaffolding) | Estimate |
|---|---|---|---|
| 1 | agent_runs + MCP + gittrack fold | ~235 (table) + ~900 (MCP/gittrack, minus render/test) + new daemon routes | 1 wave, small-medium |
| 2 | Calibration | ~731 (mostly 1:1 pure-function port) + new tables | 1 wave, small |
| 3 | Context bundle | ~498 (bundle assembly, reusing existing types) | 1 wave, small |
| 4 | Search + embeddings | ~773 (embed 195+117, search 441, embeddings 133) + FTS5/fuzzy spike | 1 wave, medium (spike first) |
| 5 | Report + NL→report | ~492 + net-new burndown/activity fns + NL→report redesign | 1 wave, medium-large (do last, most review) |
| — | Final cleanup | delete `internal/store` (18,774), `internal/gitanalysis` (879), `internal/crypto` (99), `internal/config` (419), `internal/db` (124), `internal/llm`'s dead catalog/gateway (~283), root `migrations/` (1,643 SQL), `cmd/migrate` (379), `go.mod`/`go.sum`, `go-gate.sh`, CI `go:` job | 1 short wave — deletion + gate re-verification, no new logic |

**This is several waves, not one.** Five domains with one real (if narrow)
dependency edge (2→5), one open spike (3's fuzzy fallback), one genuine
security redesign (1b), and a final cleanup wave that is mechanical but not
zero-effort (re-measuring `go-gate.sh`'s floors down to zero and then deleting
it, updating CI, re-running the full `cargo test --workspace` +
`scripts/go-gate.sh` gates one domain at a time so a regression is attributable
to the wave that caused it). Six waves total including cleanup: 4 → 2 → 5 → 3 →
1 → cleanup, per §7's ordering (renumbered here by wave sequence, not by table
row). Recommend dispatching them exactly that way, one at a time, each ending
with `go build ./...` / `go vet ./...` / `go-gate.sh` (floors ratcheted down as
files are deleted) / `cargo build --workspace` / `cargo test --workspace` all
green before starting the next.

No part of this looks like it should not be done — the five domains are real,
wanted, and each has a clear Rust landing spot. The one piece worth a founder
decision before dispatch, beyond the two flagged spikes (fuzzy-search mechanism,
MCP auth-scope reuse), is naming: whether `gittrack`/`gitstate-mcp` become new
`gitstate-cli`/`gitstate-daemon` surface or stay standalone binaries. Either is
fine engineering; it's a product-surface call, not a technical one.

---

## Verification (this pass, plan-only)

Confirmed clean before writing any of the above (baseline) and re-confirmed
after (no files under `crates/`, `cmd/`, `internal/`, `web/`, `go.mod`, or CI
were touched — only this file and a pointer in `decisions.md` were added):

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `scripts/go-gate.sh` — **GO GATE PASSED** — 12 packages built + vetted, 105
  files gofmt-clean, 10 packages tested with `-race`, 106 tests passed, 46
  DB-gated tests skipped (matches `EXPECTED_DB_SKIPPED_TESTS`).
- `cargo build --workspace` — clean.
- `cargo test --workspace` — **218 tests passed**, 0 failed (matches "218 tests
  last measured").
- `web/`: `npx eslint .` — exit 0.
- `web/`: `npm run check:lint-config` — all three assertions (type-aware linting
  live, TypeScript pinned at 6.0.3, 56 files linted ≥ floor of 40) pass, exit 0.
