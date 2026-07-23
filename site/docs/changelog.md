# Changelog

All notable changes to gitstate. Format loosely follows [Keep a Changelog](https://keepachangelog.com);
this project uses [Semantic Versioning](https://semver.org).

## [0.1.0] — 2026-07-23

The **standalone, local-first transform**. gitstate went from a Go + React + Postgres multi-tenant SaaS
to a Rust desktop app in the VulOS suite style — keeping the essence (derive true project state,
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

- Relicensed from AGPL-3.0 (+ commercial EE tier) to **MIT OR Apache-2.0**, matching the VulOS suite.
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
