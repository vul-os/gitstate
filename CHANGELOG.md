# Changelog

All notable changes to gitstate are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — the transform to local-first

gitstate is being rebuilt from a multi-tenant Go+Postgres+React SaaS into a **standalone, local-first,
peer-to-peer desktop app** in the vulos suite style (`slipscan` / `diwan` / `wede`). The product
essence is unchanged — *derive true project state, effort, contribution, and classification directly
from git and your forge* — but the delivery flips: no multi-tenant server, no Postgres SaaS, no
billing-collection cloud. It runs on your machine.

### Changed
- **Relicensed AGPL-3.0 → MIT OR Apache-2.0** (at your option), matching every sibling in the vulos
  suite. Root now carries `LICENSE-MIT` and `LICENSE-APACHE`.
- **Dropped the `ee/` commercial Enterprise tier.** With no multi-tenant service to fence off, the
  open-core split (Paystack billing, cross-org super-admin behind the `ee` build tag) no longer
  applies. Its history remains in git.
- Rewrote the project identity — README, roadmap, decisions, docs — to the standalone local-first +
  P2P story.
- CLI commands that take a repo now accept the full id, an unambiguous id prefix, or the slug
  (`gitstate state atlas-api`); an ambiguous reference lists the candidates instead of guessing.
- `gitstate contributions` prints merged display names (agents marked) rather than raw contributor ids.

### Added
- **Analytics in one round-trip** — `/api/analytics` (heatmap, weekly volume, cycle-time and
  throughput series, work-kind/state/label slices, headline totals) behind the Dashboard and Insights
  screens, with the window anchored on the newest commit rather than wall-clock now.
- **Eng Health** (`/api/health-metrics`) — DORA-flavoured cycle time, a change-failure *text* proxy,
  merge frequency, a labelled deploy proxy, bus factor, review coverage and quality signals.
- **Involvement** (`/api/involvement`) and a cross-repo **contribution rollup**, plus persisted
  six-dimension weights (`GET`/`PUT /api/weights`, `POST /api/weights/reset`) driving a live tuner in
  the UI and `gitstate contributions --weights`.
- **Derived Board** — open / in progress / merged / done, read-only by design.
- **People** — identities merged from commit emails, each `@username` shown with its linked addresses.
- **`gitstate-tracker`** — Jira and Linear import: their public APIs called from your machine with
  your own personal token (stored locally, redacted on read), plus a CSV/JSON export path that makes
  no network calls at all.
- **`gitstate seed --demo`** — a deterministic synthetic dataset, and a Playwright pipeline
  (`web/scripts/screenshots.mjs`) that captures every screen from it in dark and light.
- Read-only `GET /api/repos/{id}/classifications` and `/api/repos/{id}/effort`, so the Classify screen
  shows what has already been judged instead of an empty state.
- **Rust Cargo workspace** (`crates/*`) modeled on `slipscan`: `gitstate-core` (pure domain + traits),
  `gitstate-git` (git2-rs derivation), `gitstate-forge` (`gh`/`glab` + REST/GraphQL), `gitstate-classify`
  (local LLM + signed taxonomy + heuristic fallback + local personalization), `gitstate-store`
  (rusqlite), `gitstate-daemon` (axum: JSON API + SPA), `gitstate-cli` (clap), and `gitstate-sync`
  (P2P CRDT — **excluded** from the default workspace, behind an optional `sync-dmtap` feature).
- **Tauri desktop shell** (`apps/desktop`) that boots the daemon in-process and reuses the existing
  React `web/` UI — the desktop app and the headless daemon serve the *same* JSON API.
- **Signed taxonomy** — a versioned, content-addressed, ed25519-signed category tree shipped as data,
  verified fail-closed against a pinned key.
- New static marketing/docs `site/`, published at `vulos.org/products/gitstate`.

### Fixed
- **Remote ops are now replayed into rows, not just logged.** `gitstate_sync::apply_op` appended a
  merged op to `sync_ops` and stopped there, so two peers could exchange a whole history and neither
  one's contexts or categories moved — while `crdt.rs` documented an "op-log replay" that did not
  exist. Ingest is now `Store::merge_sync_op`: it replays the op into the context/category rows under
  the documented rules (per-field LWW by `Hlc`, add-wins OR-Set over the member add/remove clocks,
  whole-document tombstone that a strictly later write resurrects) **and** records it in the log, in
  one transaction. It uses the per-field clock maps, member clocks and tombstone clock the schema
  already carried — no migration. Merging is commutative and idempotent, pinned by a test that
  replays a six-op set in all 720 arrival orders and asserts byte-identical state, then replays it
  again and asserts nothing changed. Re-delivering an op no longer grows the log either.
- **CI covers the in-tree Go tree.** 243 Go files across 37 packages (29 with tests) had no job at
  all. A new `go` job runs build, vet, gofmt and `go test -race` via `scripts/go-gate.sh`, which
  fails closed and asserts coverage counts so it cannot pass by checking nothing.
- **`web/npm ci` was broken** — the lockfile pinned `@emnapi/wasi-threads@1.2.2` against a
  `1.2.3` requirement in the `@tailwindcss/oxide-wasm32-wasi` bundle, which took down every JS job
  including the e2e preflight. Lockfile regenerated (one transitive optional patch bump; no direct
  dependency and no major version changed).
- Documented `cargo build -p gitstate-sync …` in CONTRIBUTING and P2P-CONTEXTS could never have
  worked: the crate is excluded from the workspace, so `-p` cannot address it. Both now show the
  `--manifest-path` form the Makefile uses.
- **The HLC receive rule.** Ingesting a remote op now folds its clock into the local one
  (`Hlc::observe`), so the next local edit sorts *after* the op it causally follows even when this
  machine's wall clock trails the peer's. Previously the local clock advanced only from its own last
  reading, so an edit made in reply to a peer's write could mint a *lower* clock and lose to it under
  LWW forever. A remote clock more than `HLC_SKEW_MS` (±120 s, the bound the shared DMTAP engine
  applies) ahead of ours is still recorded but not followed. The total order itself is unchanged —
  `(wall_ms, counter, peer)`, the suite's rule — and is now pinned by a permutation test.

### Removed
- SaaS deploy artifacts: `Dockerfile`, `docker-compose.yml`, `deploy/` (fly.toml + systemd unit), and
  the SaaS `config.example.yaml`. gitstate no longer targets a hosted deployment.
- The AGPL `LICENSE` file (replaced by the dual MIT/Apache licenses).

### Kept (staged port)
- `internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` are retained **byte-for-byte** as the
  reference source for a staged port of the remaining Go domains (evidence-invoice-as-local-report and
  NL→report; DORA, effort and involvement have since been ported) into the Rust crates. See
  [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md). Nothing under those paths is edited until its
  Rust replacement passes parity.

---

_Prior to the transform, gitstate shipped as a multi-tenant Go+Postgres SaaS with Row-Level Security
tenancy, JWT auth, Paystack billing (EE), and a server-rendered super-admin console. That history is
preserved in the git log and in the still-in-tree Go source._
