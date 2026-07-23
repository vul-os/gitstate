# gitstate — Build Progress (live)

Peekable status for the **transform**: turning the legacy Go+React+Postgres multi-tenant SaaS into a
standalone, local-first, peer-to-peer desktop app (Rust core + Tauri + React + headless daemon). Roadmap
and rationale: [ROADMAP.md](ROADMAP.md), [decisions.md](decisions.md).

**Mode:** parallel agents on disjoint ownership sets (see the transform spec); one writer per area to
avoid collisions.

## Transform status

| Area | Scope | State |
|---|---|---|
| **Relicense** | AGPL-3.0 → MIT OR Apache-2.0; drop `ee/` Enterprise tier | ✅ done |
| **SaaS teardown** | remove `Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml` | ✅ done |
| **Narrative** | README, ROADMAP, CHANGELOG, decisions, SECURITY, CONTRIBUTING, docs → local-first | ✅ done |
| **rust-domain** | `gitstate-core` (types/traits/derive/taxonomy), `gitstate-classify`, `gitstate-store`, workspace manifest | 🔄 in progress |
| **rust-integration** | `gitstate-git`, `gitstate-forge`, `gitstate-daemon`, `gitstate-cli`, `gitstate-sync`, `apps/desktop` | 🔄 in progress |
| **web** | repoint React `web/` at the daemon JSON API; remove auth/org/billing surfaces | 🔄 in progress |
| **site** | new static marketing/docs site in the suite house style | 🔄 in progress |
| **cloud-gh** | CI (rust + web, no Postgres); register `gitstate` in vulos-cloud site collection | 🔄 in progress |
| **verify** | compile + trait/API coherence + endpoints match the spec; taxonomy dev keypair | 🔄 in progress |

## Kept in-tree (staged port — do NOT edit)

`internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` are retained **byte-for-byte** as the reference
source for porting the remaining Go domains (DORA parity, effort/estimation, involvement,
evidence-invoice-as-local-report, NL→report) into the Rust crates. Each Go domain is removed only once
its Rust replacement passes parity. See [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md).

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
