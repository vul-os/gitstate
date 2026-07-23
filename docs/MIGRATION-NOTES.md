# Migration notes — the staged port

gitstate was, until the transform, a **multi-tenant Go + Postgres + React SaaS**: tenancy enforced by
PostgreSQL Row-Level Security, JWT auth with rotating refresh tokens, optional Google/Microsoft OAuth,
a git engine reading commits/PRs, LLM diff-difficulty sizing, DORA metrics, a Paystack billing tier
behind an `ee` build tag, a server-rendered super-admin console, and a fly.io deploy.

It is being rebuilt as a **standalone, local-first, peer-to-peer desktop app** (Rust core + Tauri +
React + a headless daemon). The product essence is unchanged; the delivery flips off the cloud and
onto the user's machine. This document explains why a large chunk of Go is *still in the tree*.

## Why the Go server is still here

The Go server is kept **byte-for-byte in-tree** — under `internal/`, `cmd/`, and `migrations/`, with
`go.mod` and `go.sum` — for a **staged port**. Rather than delete it and reimplement from memory, we
port its still-valuable domain logic to Rust one area at a time, using the Go source as the reference
implementation. A Go domain is removed only once its Rust replacement passes parity, in a dedicated
commit.

**Untouchable during the port** (no agent edits these): `internal/**`, `cmd/**`, `migrations/**`,
`go.mod`, `go.sum`. They compile and run exactly as before; they are simply no longer the product's
front door.

## What was removed vs. kept vs. ported

| Legacy piece | Disposition |
|---|---|
| AGPL `LICENSE`, `ee/` (Paystack billing, cross-org admin) | **Removed.** No multi-tenant service to fence; relicensed MIT OR Apache-2.0 (see [decisions.md](../decisions.md) T10). |
| `Dockerfile`, `docker-compose.yml`, `deploy/`, `config.example.yaml` | **Removed.** No hosted deploy target; the daemon binds `127.0.0.1`. |
| Postgres + RLS tenancy, JWT auth, OAuth, super-admin | **Retired.** A single-user local app has no tenants, sessions, or cross-org admin. Source kept in-tree until fully unwound. |
| `internal/git` (walk, diff, blame, lead time) | **Ported** → `gitstate-git` (git2-rs), extended to blame-survival + SZZ + the six-dimension model. |
| `internal/sync` (GitHub/GitLab) | **Ported** → `gitstate-forge` (`gh`/`glab` + REST/GraphQL), local-credentials-only. |
| `internal/llm` (diff-difficulty, status synthesis) | **Porting** → `gitstate-classify` (local LLM + heuristic + personalization). |
| `internal/metrics` (cycle time, involvement) | **Porting** → `gitstate-git` derivation (DORA + six dimensions). |
| `internal/billing` (evidence invoice with visible gaps) | **Reframed.** The git-evidence invoice was genuinely useful; if ported it returns as an **optional local report** — never a payment/charging service (T12). |
| `internal/report` (NL→report, SELECT-only) | **Planned port** against the local SQLite store. |
| Postgres `migrations/` (root) | **Kept, unused by Rust.** The Rust store's forward-only migrations live in `crates/gitstate-store/migrations/` so they never collide with this directory. |

## Migration collision guard

There are two migration sets now, and they must not fight:

- **`migrations/`** (repo root) — the legacy Go Postgres migrations. Kept verbatim; the Rust code
  never reads them.
- **`crates/gitstate-store/migrations/`** — the SQLite migrations for the local app. This is the
  **only** place the Rust store looks. Keeping them inside the crate is a deliberate rule (T11) so a
  forward-only Rust migration is never confused with a Postgres one.

## Data migration for existing SaaS users

There is intentionally **no** automated import from a hosted gitstate instance into the local app:
the SaaS stored multi-tenant, per-org data behind auth, while the local app derives everything from
*your* git and forge on *your* machine. To reproduce your view locally, add your repos
(`gitstate repo add …`) and scan — the state, contributions, and classifications are re-derived from
the real ledger (git), which is exactly the point.

## Tracking the port

Progress is tracked phase-by-phase in [ROADMAP.md](../ROADMAP.md) (Phase 4 — *Staged port of the
legacy Go domains*) and live in [PROGRESS.md](../PROGRESS.md). Each ported domain gets a parity check
against the Go reference before the Go source is retired.
