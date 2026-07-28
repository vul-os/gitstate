# gitstate Roadmap

The destination: a **standalone, local-first, peer-to-peer** project-intelligence tool — a Rust core,
a Tauri desktop app, a headless daemon peer, git + forge read locally, classification on your own
LLM, and CRDT-synced contexts. **No multi-tenant server, no Postgres SaaS, no billing cloud, ever.**

This roadmap is the single source of truth for *what* we build and *in what order*. Product and
architecture rationale lives in [decisions.md](decisions.md); live build status lives in
[PROGRESS.md](PROGRESS.md). The interface contract lives in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

The old multi-tenant Go+Postgres stack — the RLS tenancy model, the super-admin console, the fly.io
deploy — lives on **in git history and, for the staged port, still in-tree** under `internal/`, `cmd/`,
and `migrations/`, as the source we port from. The billing/invoicing/accounting/COGS layer (Paystack
charging, client invoices, accounting-provider sync, cloud cost reconciliation) had no path forward in
a local-first single-tenant app and has been removed outright rather than staged; it lives on only in
git history. See [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md).

---

## 0. The wedge (why every feature exists)

Current tools (Jira, Linear, ClickUp, ZenHub) are a **manually-maintained fiction** sitting next to
git. Estimates are ~30% wrong (and have been for 40 years), velocity is gamed the moment it's a
target, and timesheets are reconstructed Friday from memory. **Git is the real ledger.** gitstate's
job: **stop asking humans to invent numbers — derive them from git and the forge** — and make
whatever fiction remains *explicit*. Three disciplines constrain every decision:

1. **Derived, not entered** — state comes from git (merged = done, PR open = in progress).
2. **Measure work, not workers** — contribution is texture across six dimensions, never a rank.
3. **Evidence-based, gaps visible** — effort is a judged read of the change; what git can't see is flagged, never invented.

And one delivery flip that defines this rebuild: **it runs on your machine, not our server.**

---

## Phase 0 — The transform ✅

Turn the legacy Go+React+Postgres multi-tenant SaaS into a standalone local-first desktop app in the
vulos suite style (`slipscan` / `diwan` / `wede`).

- [x] Relicense **AGPL-3.0 → MIT OR Apache-2.0**; drop the `ee/` commercial Enterprise tier.
- [x] Remove SaaS deploy artifacts (`Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml`).
- [x] Rewrite the project identity (README, roadmap, decisions, docs) to local-first + P2P.
- [x] Rust Cargo workspace (`crates/*`) modeled on `slipscan` — core, git, forge, classify, tracker, store, daemon, cli, sync.
- [x] Tauri shell (`apps/desktop`) that boots the daemon and reuses the React `web/` UI.
- [x] Repoint `web/` at the daemon JSON API; remove the multi-tenant auth/org/billing surfaces.
- [x] New static marketing/docs site (`site/`) published at `vulos.org/products/gitstate`.
- [x] Keep `internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` compiling for the staged port; the billing/invoicing/accounting/COGS layer was cut outright (no port planned).

Live status: [PROGRESS.md](PROGRESS.md).

---

## Phase 1 — Standalone local app ✅

The foundation everything plugs into: a machine that derives, classifies, and stores locally.

- [x] **gitstate-core** — domain types + the four traits (`ForgeClient`, `Classifier`, `Store`, `SyncEngine`) + pure derivation helpers, no I/O.
- [x] **gitstate-git** — git2-rs: open/walk/diff, blame survival, SZZ bug-intro, project-state and six-dimension contribution derivation.
- [x] **gitstate-forge** — GitHub + GitLab via `gh`/`glab` (REST/GraphQL token fallback): PRs, issues, reviews. Typed error when the CLI is missing.
- [x] **gitstate-store** — rusqlite persistence, forward-only migrations, WAL, a data dir resolved from the OS + `GITSTATE_DATA_DIR`.
- [x] **gitstate-daemon** — axum server: JSON API + SPA static serving; `serve` (fixed port) and `serve_ephemeral` (Tauri) paths.
- [x] **gitstate-cli** — clap: `serve`, `repo`, `state`, `contributions`, `classify`, `effort`, `context`, `category`, `taxonomy`, `data`.
- [x] **apps/desktop** — Tauri shell over the daemon; the React UI resolves the daemon base URL injected at startup.
- [x] **web/** — the kept React app, repointed at the daemon; auth/org/billing hooks removed or no-op'd (single-user local app).

---

## Phase 2 — Classification, effort &amp; the signed taxonomy ✅

Honest, local, decentralized labeling.

- [x] **gitstate-classify** — `LlmClassifier` (llmux / OpenAI-compatible, env-driven) + `HeuristicClassifier` (always available, deterministic).
- [x] **LLM diff-difficulty** — judge the *shape* of a change (1–13, fibonacci-ish), never line count; heuristic fallback.
- [x] **Signed taxonomy** — versioned, content-addressed, ed25519-signed category tree shipped as embedded data; `verify()` against a pinned key, **fail-closed**.
- [x] **Local personalization** — corrections train a per-box prior that re-ranks classifications; replaces any pooled fine-tuning.
- [x] Runtime taxonomy override via `GITSTATE_TAXONOMY_PATH`; production re-signs with the release key.

---

## Phase 3 — Peer-to-peer contexts &amp; categories (model done, transport open)

Share the smarts (working sets and labels), never the code.

- [x] **CRDT model in core** — `SyncOp` op envelope; contexts and categories as LWW scalars + OR-Sets with a hybrid logical clock; add-wins, tombstones, resurrection.
- [x] **gitstate-sync** (excluded crate, feature `sync-dmtap`) — `CrdtSyncEngine`, op derivation, and an idempotent merge that replays a remote op into the context/category rows (`Store::merge_sync_op`): local edits and remote ops reach the same tables under the same clock comparison.
- [ ] **Transport** — P2P over the shared vulos/DMTAP sync substrate rather than a bespoke stack; signed, no central hub.
- [x] **Context export/import** — a portable JSON working set, shareable out-of-band even without the sync feature built.
- [x] Convergence tests — commutative + idempotent op application; replay in any order yields identical state.

---

## Phase 4 — Staged port of the legacy Go domains

Retire the in-tree Go server by porting its still-valuable logic to Rust, one domain at a time. Until
a domain is ported, the Go source stays in-tree as the reference (never edited).

- [x] **DORA parity** — cycle-time p50/p90 and a labelled change-failure proxy derived in `gitstate-git` + `gitstate-core::health`, alongside bus factor, review coverage and quality signals.
- [x] **Effort/estimation parity** — diff-difficulty judging in `gitstate-classify` (LLM + deterministic heuristic). Open follow-up: resolve real per-PR add/delete counts against the worktree so the heuristic has more than path signal.
- [x] **Involvement parity** — per-repo and per-person involvement plus the six-dimension model with tunable weights and a cross-repo rollup.
- [ ] **Reporting / NL→report** — port the SELECT-only queryable report path against the local SQLite store.
- [ ] Once a domain's Rust port passes parity, remove the corresponding Go source in a dedicated commit.

---

## Phase 5 — Packaging &amp; the site (current)

- [ ] Tauri installers for macOS (`.dmg`), Windows (`.msi` / setup), Linux (`.AppImage` / `.deb`) + standalone CLI/daemon binaries.
- [ ] Tag-triggered release CI (version-match guard, draft releases).
- [x] `site/` static marketing + docs on the app's own design tokens; published at `vulos.org/products/gitstate`.
- [x] Real screenshots of the desktop app, captured by `web/scripts/screenshots.mjs` against `gitstate seed --demo` (dark + light), replacing the legacy SaaS captures.

---

## Later / optional — the dormant discovery coordinator

Deliberately **not** built now; kept only as a seam so it can be added without reshaping the core.

- [ ] **Optional coordinator** — the *only* place "needs a view of strangers you'll never meet" features could live: cross-population "trending / similar / others tagged".
- [ ] If ever built, it is opt-in, untrusted-by-design, and reads only aggregate signals — never your repos, diffs, or contributions.
- [ ] No anti-spam/sybil tier and no pooled fine-tuning are planned — both are taxes on a discovery layer that does not exist.

The rule that keeps the seam dormant: *everything a git tool is for is local + P2P; only stranger-facing discovery would ever belong to a coordinator.*

---

## Definition of done (per phase)

- `cargo build --workspace` green **without** pulling P2P/sync deps; `cargo test` green.
- `cd web && npm run build` green; the daemon serves the embedded SPA with a working `/api`.
- A local scan of a repo makes **zero** network calls; forge scans use only the user's `gh`/`glab`/token.
- The desktop app and `gitstate serve` expose the **same** API from the **same** core.
- Every feature traceable to a wedge discipline in §0; nothing forces a human to invent a number.
