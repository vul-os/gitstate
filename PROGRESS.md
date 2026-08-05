# gitstate — Build Progress (live)

Peekable status for the **transform**: turning the legacy Go+React+Postgres multi-tenant SaaS into a
standalone, local-first, peer-to-peer desktop app (Rust core + Tauri + React + headless daemon). Roadmap
and rationale: [ROADMAP.md](ROADMAP.md), [decisions.md](decisions.md).

## Transform status

| Area | Scope | State |
|---|---|---|
| **Relicense** | AGPL-3.0 → MIT OR Apache-2.0; drop `ee/` Enterprise tier | ✅ done |
| **SaaS teardown** | remove `Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml` | ✅ done |
| **Narrative** | README, ROADMAP, CHANGELOG, decisions, SECURITY, CONTRIBUTING, docs → local-first | ✅ done |
| **rust-domain** | `gitstate-core` (types/traits/derive/analytics/health/taxonomy), `gitstate-classify`, `gitstate-store` | ✅ done |
| **rust-integration** | `gitstate-git`, `gitstate-forge`, `gitstate-tracker`, `gitstate-daemon`, `gitstate-cli`, `apps/desktop` | ✅ done |
| **web** | React repointed at the daemon JSON API; auth/org/billing surfaces removed; 15 screens live | ✅ done |
| **site** | static landing + self-contained docs viewer in `site/`, on the app's own design tokens | ✅ done |
| **screenshots** | `gitstate seed --demo` + a Playwright pipeline capturing every screen, dark and light | ✅ done |
| **cloud-gh** | CI (rust + web, no Postgres); register `gitstate` in the vulos.org site collection | 🔄 in progress |
| **packaging** | signed installers: macOS `.dmg`, Linux `.AppImage`/`.deb`, Windows `.msi` | ⬜ not started |
| **sync transport** | HTTP peer transport, manual enrolment, per-op signatures, mutual request auth; driven between two nodes over a non-loopback address | ✅ done |
| **Go retirement** | port the remaining legacy domains, then delete `internal/`, `cmd/`, root `migrations/` | ✅ done |

## What works today

- `cargo build --workspace` → core, git, forge, classify, tracker, store, daemon, CLI. All tests green.
- `gitstate seed --demo && gitstate serve` → every screen populated, with no repo and no network.
- Derivation: project state, six-dimension contribution (+ tunable weights), effort, classification.
- Analytics: dashboard/insights rollups, Eng Health (DORA proxies, bus factor, review coverage,
  quality), involvement, cross-repo contribution rollup.
- Jira and Linear import with your own token, or fully offline from a CSV/JSON export.
- Contexts and categories as CRDTs in the local op log; taxonomy verified fail-closed.

## Known gaps (stated, not hidden)

- **No packaged installers yet** — build from source.
- **Effort judging sees file/path shape only** for forge items; exact per-PR add/delete counts need the
  PR's base/head resolved against the local worktree. Without an LLM endpoint the heuristic therefore
  collapses low-signal items toward difficulty 1.0. The `method` and `confidence` fields always say
  which judge produced a row.
- **`change_failure_rate` is a text proxy** (revert/hotfix/rollback in the title or labels), and the
  deploy metric counts merge commits — both are labelled as proxies wherever they appear.
- **P2P sync has no confidentiality of its own.** Both ends and every individual op are
  authenticated over plain HTTP, but ops are not encrypted: on an `http://` peer URL, context names,
  notes and tags are readable in flight. A terminating reverse proxy is the documented answer
  (`docs/DEPLOYMENT.md` §2); gitstate does not implement TLS.
- **The merge algebra is gitstate's own, not the shared KOTVA engine.** It is held against that engine
  in `crates/gitstate-sync/tests/shared_engine_parity.rs` — LWW parity proven over every arrival
  order, and the two divergences (an LWW-element-set rather than §4.3's observed-remove OR-Set; a
  resurrecting tombstone rather than a §4.5 death certificate) asserted rather than described. Closing
  either is a **wire** change, not a merge change.
- **Sync key rotation is manual.** A node's keypair lives in its SQLite file; rotating it means
  re-enrolling with every peer by hand, and removing an enrolment does not retract ops already
  replicated.

## Go tree — retired

`internal/`, `cmd/`, root `migrations/`, `go.mod` and `go.sum` were kept in-tree byte-for-byte as the
reference source for a staged port and are now **deleted** — every domain that had a Rust equivalent
was ported first and parity-checked against the Go reference before its source was removed; the rest
was either already superseded or SaaS-only-and-dropped. gitstate is pure Rust + TypeScript now. See
[docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md) and [docs/PORT-PLAN.md](docs/PORT-PLAN.md) for the
full domain-by-domain record.

## Contract (so parallel agents stay compatible)

- `gitstate-core` is the single source of truth for domain types + the four traits (`ForgeClient`,
  `Classifier`, `Store`, `SyncEngine`); everyone else consumes it as a read-only contract.
- The daemon serves both the desktop shell and headless mode from **one** JSON API (default
  `127.0.0.1:7473`; Tauri uses an ephemeral port injected into the webview as `window.__GITSTATE_API__`).
- `cargo build --workspace` must stay network-free and must not depend on another *product*.
  `gitstate-sync` is an ordinary member; the shared merge engine it is checked against is the
  published `kotva-sync` from crates.io, as a **dev** dependency. `Cargo.lock` contains no `git`
  sources at all — verify with `grep 'source = "git' Cargo.lock` finding nothing.
- The web client routes every call through `web/src/lib/api.js`; JSON is snake_case throughout,
  matching the domain serde.

---

_The pre-transform build log (the 8-wave autonomous Go+Postgres SaaS build — RLS tenancy, JWT auth,
git engine, metrics, Paystack billing EE, super-admin, deploy) is preserved in the git history._
