# Changelog

All notable changes to gitstate. Format loosely follows [Keep a Changelog](https://keepachangelog.com);
this project uses [Semantic Versioning](https://semver.org).

## [Unreleased]

### Added

- **Analytics in one round-trip** — `/api/analytics` (heatmap, weekly volume, cycle time, throughput,
  work-kind/state/label slices, headline totals) behind the Dashboard and Insights screens.
- **Eng Health** — `/api/health-metrics`: DORA-flavoured cycle time, a change-failure text proxy,
  merge frequency, a labelled deploy proxy, bus factor, review coverage and quality signals.
- **Involvement** and a cross-repo **contribution rollup**, plus persisted six-dimension
  [weights](analytics.md) with a live tuner in the UI (`GET`/`PUT /api/weights`, `--weights` on the CLI).
- **Derived Board** — open / in progress / merged / done, read-only by design.
- **People** — identities merged from commit emails, each `@username` shown with its linked addresses.
- **Jira & Linear import** (`gitstate-tracker`): the vendors' APIs called from your machine with your
  own personal token, plus a fully offline CSV/JSON export path. See [import](import.md).
- **`gitstate seed --demo`** — a deterministic synthetic dataset, and a Playwright pipeline that
  captures every screen from it in both themes.
- Read-only `GET /api/repos/{id}/classifications` and `/effort`, so the Classify screen can show what
  has already been judged without triggering a new pass.

### Changed

- CLI commands that take a repo now accept the full id, an unambiguous id prefix, or the slug —
  `gitstate state atlas-api` instead of pasting a uuid.
- `gitstate contributions` prints merged display names (agents marked) instead of raw contributor ids.

## [0.1.0] — 2026-07-23

The **standalone, local-first transform**. gitstate went from a Go + React + Postgres multi-tenant SaaS
to a Rust desktop app in the Vulos suite style — keeping the essence (derive true project state,
effort, contribution, and classification from your own git + forge) and flipping the delivery.

### Added

- Rust Cargo workspace: `gitstate-core` (pure domain), `gitstate-git` (git2 derivation),
  `gitstate-forge` (gh/glab + REST), `gitstate-classify` (LLM + heuristic + personalization),
  `gitstate-store` (SQLite), `gitstate-daemon` (axum), `gitstate-cli`, and the excluded, optional
  `gitstate-sync`.
- Desktop app (`apps/desktop`, Tauri) that starts the daemon in-process and reuses the React UI.
- Headless daemon (`gitstate serve`) serving `web/dist` + the JSON API on port `7473`.
- Six-dimension contribution model (shipped, review, effort, quality, ownership, durability) shown as
  texture, never a rank.
- Effort judged from diff difficulty (LLM or deterministic heuristic), not line count.
- Signed, versioned, content-addressed taxonomy shipped as data, verified fail-closed against a pinned
  ed25519 key.
- Contexts (saved working sets) and categories as CRDT documents — LWW scalars, OR-Set members,
  tombstoned deletes — in a SQLite op log.
- Local personalization that learns each box's conventions, replacing pooled fine-tuning.

### Changed

- Relicensed from AGPL-3.0 (+ commercial EE tier) to **MIT OR Apache-2.0**, matching the Vulos suite.
- Frontend kept as React, repointed from the multi-tenant SaaS backend to the local daemon JSON API;
  org/JWT/billing surfaces removed.

### Removed

- The multi-tenant server, Postgres schema usage, billing-collection cloud, and org/seat model.
- The `ee/` commercial tier and SaaS deploy artifacts (Dockerfile, docker-compose, deploy manifests).

### Notes

- The Go `internal/` and `cmd/` trees remain in-tree, untouched, for a staged port — they are not built
  by the Rust workspace.
- The default taxonomy is signed with a development key; production re-signs with the release key.

Next: [Roadmap](roadmap.md) · [Getting started](getting-started.md)
