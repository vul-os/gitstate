# gitstate Roadmap

The destination: a **standalone, local-first, peer-to-peer** project-intelligence tool — a Rust core,
a Tauri desktop app, a headless daemon peer, git + forge read locally, classification on your own
LLM, and CRDT-synced contexts. **No multi-tenant server, no Postgres SaaS, no billing cloud, ever.**

This roadmap is the single source of truth for *what* we build and *in what order*. Product and
architecture rationale lives in [decisions.md](decisions.md); live build status lives in
[PROGRESS.md](PROGRESS.md). The interface contract lives in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

The old multi-tenant Go+Postgres stack — the RLS tenancy model, Paystack billing, the super-admin
console, the fly.io deploy — lives on **in git history and, for the staged port, still in-tree** under
`internal/`, `cmd/`, and `migrations/`. Nothing there is deleted; it is the source we port from. See
[docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md).

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

## Phase 0 — The transform ✅ / in progress

Turn the legacy Go+React+Postgres multi-tenant SaaS into a standalone local-first desktop app in the
vulos suite style (`slipscan` / `ofisi` / `wede`).

- [x] Relicense **AGPL-3.0 → MIT OR Apache-2.0**; drop the `ee/` commercial Enterprise tier.
- [x] Remove SaaS deploy artifacts (`Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml`).
- [x] Rewrite the project identity (README, roadmap, decisions, docs) to local-first + P2P.
- [ ] Rust Cargo workspace (`crates/*`) modeled on `slipscan` — core, git, forge, classify, store, daemon, cli, sync.
- [ ] Tauri shell (`apps/desktop`) that boots the daemon and reuses the React `web/` UI.
- [ ] Repoint `web/` at the daemon JSON API; remove the multi-tenant auth/org/billing surfaces.
- [ ] New static marketing/docs site (`site/`) folded into `vulos-cloud` at `gitstate.<vulos-domain>`.
- [ ] Keep `internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` byte-for-byte for the staged port.

Live status: [PROGRESS.md](PROGRESS.md).

---

## Phase 1 — Standalone local app (near-term)

The foundation everything plugs into: a machine that derives, classifies, and stores locally.

- [ ] **gitstate-core** — domain types + the four traits (`ForgeClient`, `Classifier`, `Store`, `SyncEngine`) + pure derivation helpers, no I/O.
- [ ] **gitstate-git** — git2-rs: open/walk/diff, blame survival, SZZ bug-intro, project-state and six-dimension contribution derivation.
- [ ] **gitstate-forge** — GitHub + GitLab via `gh`/`glab` (REST/GraphQL token fallback): PRs, issues, reviews. Typed error when the CLI is missing.
- [ ] **gitstate-store** — rusqlite persistence, forward-only migrations, WAL, a data dir resolved from the OS + `GITSTATE_DATA_DIR`.
- [ ] **gitstate-daemon** — axum server: JSON API + SPA static serving; `serve` (fixed port) and `serve_ephemeral` (Tauri) paths.
- [ ] **gitstate-cli** — clap: `serve`, `repo`, `state`, `contributions`, `classify`, `effort`, `context`, `category`, `taxonomy`, `data`.
- [ ] **apps/desktop** — Tauri shell over the daemon; the React UI resolves the daemon base URL injected at startup.
- [ ] **web/** — the kept React app, repointed at the daemon; auth/org/billing hooks removed or no-op'd (single-user local app).

---

## Phase 2 — Classification, effort &amp; the signed taxonomy

Honest, local, decentralized labeling.

- [ ] **gitstate-classify** — `LlmClassifier` (llmux / OpenAI-compatible, env-driven) + `HeuristicClassifier` (always available, deterministic).
- [ ] **LLM diff-difficulty** — judge the *shape* of a change (1–13, fibonacci-ish), never line count; heuristic fallback.
- [ ] **Signed taxonomy** — versioned, content-addressed, ed25519-signed category tree shipped as embedded data; `verify()` against a pinned key, **fail-closed**.
- [ ] **Local personalization** — corrections train a per-box prior that re-ranks classifications; replaces any pooled fine-tuning.
- [ ] Runtime taxonomy override via `GITSTATE_TAXONOMY_PATH`; production re-signs with the release key.

---

## Phase 3 — Peer-to-peer contexts &amp; categories

Share the smarts (working sets and labels), never the code.

- [ ] **CRDT model in core** — `SyncOp` op envelope; contexts and categories as LWW scalars + OR-Sets with a hybrid logical clock; add-wins, tombstones, resurrection.
- [ ] **gitstate-sync** (excluded crate, feature `sync-dmtap`) — `CrdtSyncEngine`, op derivation, idempotent merge; one path for local edits and remote merges.
- [ ] **Transport** — P2P over the shared vulos/DMTAP sync substrate rather than a bespoke stack; signed, no central hub.
- [ ] **Context export/import** — a portable JSON working set, shareable out-of-band even without the sync feature built.
- [ ] Convergence tests — commutative + idempotent op application; replay in any order yields identical state.

---

## Phase 4 — Staged port of the legacy Go domains

Retire the in-tree Go server by porting its still-valuable logic to Rust, one domain at a time. Until
a domain is ported, the Go source stays in-tree as the reference (never edited).

- [ ] **DORA parity** — cycle-time p50/p90 and change-failure rate fully derived in `gitstate-git` (partly in Phase 1); match the legacy `internal/metrics` outputs.
- [ ] **Effort/estimation parity** — port `internal/llm` diff-difficulty prompts and calibration into `gitstate-classify`.
- [ ] **Involvement parity** — port `internal/metrics` multi-dimension involvement (texture, no score) into the six-dimension model.
- [ ] **Billing/invoice (evidence) — reframed** — the legacy `internal/billing` produced git-evidence invoices with visible gaps. Port only as an **optional, local** report generator (no Paystack, no multi-tenant charging); the billing-collection cloud is **not** rebuilt.
- [ ] **Reporting / NL→report** — port the SELECT-only queryable report path against the local SQLite store.
- [ ] Once a domain's Rust port passes parity, remove the corresponding Go source in a dedicated commit.

---

## Phase 5 — Packaging &amp; the site

- [ ] Tauri installers for macOS (`.dmg`), Windows (`.msi` / setup), Linux (`.AppImage` / `.deb`) + standalone CLI/daemon binaries.
- [ ] Tag-triggered release CI (version-match guard, draft releases).
- [ ] `site/` static marketing + docs in the suite house style; folded into `vulos-cloud` and served at `gitstate.<vulos-domain>`.
- [ ] Real screenshots of the desktop app (replacing the legacy SaaS captures).

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
