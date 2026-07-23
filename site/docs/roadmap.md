# Roadmap

gitstate's direction follows one rule: **only "needs a view of strangers you'll never meet" belongs to
an optional coordinator; everything a git tool is actually for is local + P2P.** The roadmap is shaped
by that line, not by a subscription funnel.

## Phase 0 — Standalone transform (current)

- [x] Rust workspace modeled on the VulOS siblings — pure `gitstate-core`, integration crates, one
  SQLite file.
- [x] Local-first delivery: desktop (Tauri) + headless daemon serving one JSON API, no Postgres, no
  multi-tenant server.
- [x] git2 derivation — history walk, blame survival, SZZ, diff summaries.
- [x] Forge access via `gh` / `glab` (REST with a token fallback).
- [x] Local classification (LLM + deterministic heuristic) and effort judged from the diff.
- [x] Signed, versioned, content-addressed taxonomy shipped as data, verified fail-closed.
- [x] Contexts and categories as CRDT documents in the op log.
- [x] MIT OR Apache-2.0 relicense; EE tier and SaaS deploy artifacts removed.

## Phase 1 — Sharpen the derivation

- [ ] Richer SZZ heuristics and configurable caps for very large monorepos.
- [ ] Ownership from directory-level blame concentration, tunable boundaries.
- [ ] Per-language effort priors to calibrate the heuristic without an LLM.
- [ ] Cycle-time breakdowns (coding vs. review vs. idle) surfaced in the UI.

## Phase 2 — Peer-to-peer, for real

- [ ] Wire the optional `sync-dmtap` transport end to end (device-to-device context + category sync).
- [ ] Signed context exports with provenance, so an imported working set is verifiable.
- [ ] Conflict-resolution surfacing in the UI (what merged, what was superseded).

## Phase 3 — Local learning & taxonomy tooling

- [ ] Stronger on-box personalization — per-repo priors, not just global.
- [ ] A signing/verification toolchain for publishing your own taxonomy versions.
- [ ] Optional community taxonomy packs distributed over git remotes — still data, never a service.

## Deliberately *not* on the roadmap

- **Cross-population discovery** (trending / "similar" / "others tagged") — needs a view of strangers,
  so only a dormant optional coordinator seam exists; the feature is not built.
- **Anti-spam / sybil tiers** — a tax on the unbuilt discovery layer.
- **Pooled-feedback fine-tuning** — replaced by local personalization.
- **A hosted multi-tenant service, org/seat billing, telemetry** — the honeypot the transform removed.

Next: [Changelog](changelog.md) · [Architecture](architecture.md)
