# Changelog

All notable changes to gitstate are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — the transform to local-first

gitstate is being rebuilt from a multi-tenant Go+Postgres+React SaaS into a **standalone, local-first,
peer-to-peer desktop app** in the vulos suite style (`slipscan` / `ofisi` / `wede`). The product
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

### Added
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

### Removed
- SaaS deploy artifacts: `Dockerfile`, `docker-compose.yml`, `deploy/` (fly.toml + systemd unit), and
  the SaaS `config.example.yaml`. gitstate no longer targets a hosted deployment.
- The AGPL `LICENSE` file (replaced by the dual MIT/Apache licenses).

### Kept (staged port)
- `internal/`, `cmd/`, `migrations/`, `go.mod`, `go.sum` are retained **byte-for-byte** as the
  reference source for a staged port of the remaining Go domains (DORA parity, effort/estimation,
  involvement, evidence-invoice-as-local-report, NL→report) into the Rust crates. See
  [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md). Nothing under those paths is edited until its
  Rust replacement passes parity.

---

_Prior to the transform, gitstate shipped as a multi-tenant Go+Postgres SaaS with Row-Level Security
tenancy, JWT auth, Paystack billing (EE), and a server-rendered super-admin console. That history is
preserved in the git log and in the still-in-tree Go source._
