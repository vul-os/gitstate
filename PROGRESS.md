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
| **sync transport** | wire `sync-dmtap` end to end (the CRDT model + op log already ship) | ⬜ not started |
| **Go retirement** | port the remaining legacy domains, then delete `internal/`, `cmd/`, root `migrations/` | 🔄 in progress |

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
- **P2P transport is unwired**: the CRDT semantics, op log and CLI surface exist; the `sync-dmtap`
  build has not been driven end to end between two machines.

## Kept in-tree (staged port — do NOT edit)

`internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` are retained **byte-for-byte** as the reference
source for porting the remaining Go domains (evidence-invoice-as-local-report, NL→report) into the Rust
crates. Each Go domain is removed only once its Rust replacement passes parity. See
[docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md).

## Contract (so parallel agents stay compatible)

- `gitstate-core` is the single source of truth for domain types + the four traits (`ForgeClient`,
  `Classifier`, `Store`, `SyncEngine`); everyone else consumes it as a read-only contract.
- The daemon serves both the desktop shell and headless mode from **one** JSON API (default
  `127.0.0.1:7473`; Tauri uses an ephemeral port injected into the webview as `window.__GITSTATE_API__`).
- `cargo build --workspace` must not pull the P2P/sync stack — `gitstate-sync` is excluded and behind
  the `sync-dmtap` feature.
- The web client routes every call through `web/src/lib/api.js`; JSON is snake_case throughout,
  matching the domain serde.

---

_The pre-transform build log (the 8-wave autonomous Go+Postgres SaaS build — RLS tenancy, JWT auth,
git engine, metrics, Paystack billing EE, super-admin, deploy) is preserved in the git history._
