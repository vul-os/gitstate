# gitstate — Architecture &amp; Product Decisions

Format: **Decision → Why → Consequence**. When a choice isn't covered here, pick the option that best
serves *derived-not-entered*, *measure-work-not-workers*, and *evidence-with-visible-gaps* — and,
since the transform, *local-first over hosted* — then append a new entry.

The transform decisions (T-series) supersede the earlier SaaS architecture decisions (A-series,
retained below for provenance). Where a T-entry and an A-entry conflict, the T-entry wins.

---

## Transform decisions (local-first, P2P) — current

**T1. Standalone local-first desktop app, not a SaaS.** gitstate is delivered as a Rust core over
plain SQLite, wrapped in a Tauri desktop app, plus a headless daemon that is the always-on peer. →
No multi-tenant server, no Postgres, no billing cloud, no account. → The product essence (derive
state/effort/contribution/classification from git + forge) is unchanged; only *where it runs* flips —
onto the user's machine.

**T2. One daemon serves both desktop and headless.** `gitstate-daemon` (axum) serves `web/dist` as an
SPA *and* the JSON API. The Tauri shell boots this same daemon on an ephemeral local port; the React
UI points at it. → The UI is never forked into the desktop app; there is exactly one API surface. →
`gitstate serve` (headless peer) and the desktop app are the same core with different front doors.

**T3. Forge access is local, using the user's own credentials.** GitHub/GitLab are read by shelling
the user's `gh`/`glab` CLI (REST/GraphQL only as a token fallback when the CLI is absent). → No
gitstate-hosted forge broker, no stored OAuth apps, no tenant tokens. → A plain scan of a local repo
makes zero network calls; forge scans use only the credentials already on the box.

**T4. Classification is local-only.** Work-item classification and effort judging run against the
user's LLM endpoint (llmux / any OpenAI-compatible URL via env) or a deterministic heuristic when
none is configured. → Better privacy, no cloud dependency, works offline. → Corrections train a
**local personalization** store (T6); nothing about the user's work is pooled.

**T5. Label alignment is a signed data file, not a service.** So peers agree on what a category
*means*, gitstate ships a versioned, content-addressed, **ed25519-signed taxonomy** as embedded data,
overridable via `GITSTATE_TAXONOMY_PATH`. `verify()` recomputes the content hash, checks the pinned
public key, and verifies the signature — **fail-closed**: a bad signature falls back to local-only
categories and never silently trusts. → Cross-peer agreement without a running registry. → The dev
key ships in-repo (noted below); production re-signs with the release key.

**T6. Personalization replaces pooled fine-tuning.** Each box learns its own conventions from the
user's classification feedback and re-ranks future suggestions locally. → No feedback ever leaves the
machine; there is no shared model to poison or leak into. → `record_feedback` + a local prior; no
network path exists.

**T7. Only "needs a view of strangers you'll never meet" belongs to a coordinator.** Cross-population
features — trending, "similar repos", "others tagged this" — are the *only* things that require
seeing beyond your own peers, so they are **not built**; a dormant optional coordinator seam is left
and nothing more. → No anti-spam/sybil tier (a tax on the unbuilt discovery layer) and no pooled
feedback. → Everything a git tool is actually for stays local + P2P.

**T8. Contexts and categories sync peer-to-peer as CRDTs.** The sharable unit is a **context** (a
saved working set: repos, PR refs, notes, tags); categories are shared too. Both are CRDTs (LWW
scalars + OR-Sets over a hybrid logical clock), merged over the shared vulos/DMTAP sync substrate —
never a bespoke stack, never a central hub. → Two peers converge with no authority in the middle;
derived caches (commits, contributions, project state) are *local* and never synced. → Local edits
and remote merges share one op-application path.

**T9. Replication is compiled in; reaching a peer is what is opt-in.** `gitstate-sync` was once
excluded from the workspace behind a `sync-dmtap` feature, because it carried an optional **git**
dependency on the `envoir` repository and Cargo resolves optional sources during workspace
resolution — so a plain build shelled out to that remote even with the feature off. Two things were
wrong with that. The dependency was a *product* importing a *product* to get a merge engine; and the
feature wired nothing — both transports behind it returned success and an empty list, so every
document that mentioned it described a mechanism that did not exist. The exclusion also kept
`cargo test --workspace` from ever compiling the crate's tests.

→ The engine is now the published substrate crate (`kotva-sync`, crates.io) and a **dev** dependency,
used to hold gitstate's own algebra against it; the feature and the stub transports are gone;
`gitstate-sync` is a normal member. `Cargo.lock` has no git sources. → Sync is opt-in at *enrolment*,
not at build time: no feature to compile, and no peer until an operator types a URL and a key.

**T10. Relicense to MIT OR Apache-2.0; drop the EE tier.** The suite standard is MIT OR Apache-2.0
(every sibling — slipscan, diwan, wede — matches). With no multi-tenant service, the open-core AGPL +
commercial `ee/` split is obsolete. → Root carries `LICENSE-MIT` + `LICENSE-APACHE`; the AGPL
`LICENSE` and `ee/` are removed (history preserved). → No build tags, no runtime license check.

**T11. The legacy Go server stays in-tree for a staged port.** `internal/`, `cmd/`, `migrations/`,
`go.mod`, `go.sum` are kept **byte-for-byte** as the reference we port from — DORA metrics,
effort/estimation, involvement, evidence-invoice (reframed as an optional *local* report), NL→report.
→ Nothing is lost in the pivot; each domain is ported to Rust and only then is its Go source removed.
→ The Rust SQLite migrations live inside `crates/gitstate-store/migrations/`, never at repo root, to
avoid colliding with the kept Go `migrations/`.

**T12. Billing is not rebuilt as a service.** The legacy evidence-invoice (git-backed lines, gaps
flagged for a human) was genuinely useful; the *collection* half (Paystack, USD→ZAR, multi-tenant
charging) was SaaS scaffolding. → If ported, invoicing returns only as an **optional, local** report
generator. → No payment provider, no exchange-rate service, no charging path.

> **Dev taxonomy key.** The taxonomy signature currently uses a **development** ed25519 keypair
> generated during the transform; the public key is pinned as `DEFAULT_TAXONOMY_PUBKEY` in
> `gitstate-core` and the embedded `default_taxonomy.json` is signed with the matching private key.
> This proves the fail-closed verify path end-to-end. **Production must re-sign the default taxonomy
> with the offline release key** and update the pinned constant before any signed distribution.

> **T11 resolution (2026-08-04).** Every `internal/` package and `cmd/` binary was read against its
> Rust counterpart (or lack of one) and classified PORTED / SAAS-ONLY-DROP / NOT-YET-PORTED — full
> evidence in the domain-map commit and in [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md). The
> port is **not complete**; this is a partial removal, not the "only then is its Go source removed"
> closure T11 describes for the whole tree.
>
> Removed (`git rm`, history preserved): the domains with a verified Rust equivalent —
> `internal/git`, `internal/sync`, `internal/contribution`, `internal/contributors`,
> `internal/metrics`, `internal/importer`, `cmd/seed`, `cmd/seedgit` — and everything that only ever
> served the abandoned multi-tenant SaaS server — `cmd/gitstate`, `internal/api`, `internal/admin`,
> `internal/auth`, `internal/middleware`, `internal/oauth`, `internal/calendar`, `internal/capacity`
> (package), `internal/chat`, `internal/notifications`, `internal/email`, `internal/webhooks`,
> `internal/githubapp`, `internal/web`, `internal/docs`, `internal/jobs`, `internal/analytics`.
>
> **Not removed — genuinely not yet ported, real functionality that would be lost:**
> `internal/report` (NL→report + dashboard burndown/throughput; no Rust equivalent at all),
> `internal/calibration` (empirical-Bayes effort-calibration curves; no Rust equivalent),
> `internal/embed` + `store/search.go` + `store/embeddings.go` (local semantic + fuzzy search over
> issues; no Rust equivalent), `store/agent_runs.go` + `cmd/gitstate-mcp` + `cmd/gittrack`'s
> `log-run`/`runs`/`whoami`/`context` (the agent-native write path and the MCP bridge — `gitstate-cli`
> has neither today), and `store/context_bundle.go` (token-efficient issue+PR context for an agent,
> distinct from `gitstate-cli context`'s CRDT saved-working-sets).
>
> **Kept only as scaffolding** (not wanted features in themselves, but load-bearing for the packages
> above to keep building and testing against Postgres in CI): `internal/config`, `internal/db`,
> `internal/crypto`, `internal/llm` (its diff-difficulty domain IS ported to `gitstate-classify`; the
> Go package stays because `internal/report` still imports it for status synthesis), and
> `internal/gitanalysis` (its domain IS ported to `gitstate-git`; the Go package stays only because
> `internal/store`'s persistence layer imports its types). `internal/store` itself stays whole — one
> Go package interleaving not-yet-ported files with already-ported and SaaS-only ones — rather than
> risk file-level surgery breaking the still-needed `report`/`calibration` build. `migrations/` and
> `cmd/migrate` stay to keep provisioning that Postgres schema. `go.mod`/`go.sum` are **not** removed:
> not all Go fell into PORTED/SAAS-ONLY, so the T11 exit condition ("only then is its Go source
> removed") has not been reached for the tree as a whole. `scripts/go-gate.sh`'s coverage floors were
> re-measured against the smaller tree (12 packages / 105 files / 10 tested, was 37/243/29) so the
> `go:` CI job keeps meaning something instead of asserting stale numbers.
>
> **Port plan (2026-08-05, branch `port-plan`, plan-only — no code moved).** The remaining five
> domains (report, calibration, search/embeddings, agent_runs/MCP, context_bundle) all fit the
> single-file SQLite model with no architectural gap; see [docs/PORT-PLAN.md](docs/PORT-PLAN.md) for
> the full per-domain table, dependency graph, drop candidates (`store/planning.go` — zero live
> callers; `internal/crypto` — no path to any real domain, both missed by the file-level granularity
> of the 2026-08-04 sweep), third-party-dependency check (none need a new crate — even the LLM client
> and the embedder already have a Rust home or a dependency-free path), and the recommended six-wave
> order (agent_runs/MCP → calibration → context_bundle → search/embeddings → report/NL→report →
> final `internal`/`migrations`/`go.mod` cleanup).
>
> **Wave 1 shipped (2026-08-05, branch `port-agent-runs`): `store/agent_runs.go` + `cmd/gitstate-mcp`
> + `cmd/gittrack`'s `log-run`/`runs`/`whoami`.** `org_id`/RLS dropped — one tenant, nothing left to
> scope (see the new `agent_runs` table's migration comment for the full reasoning). `gittrack` and
> `gitstate-mcp` are folded into `gitstate-cli` as planned, not shipped as new standalone binaries:
> `gitstate agent log-run`/`runs`/`whoami` and `gitstate mcp`. Both call `gitstate_daemon::ops`
> **in-process** against the local SQLite file — the same pattern every other `gitstate` subcommand
> already uses — rather than over HTTP with a token, which is *also* the resolution to the wave's
> flagged MCP auth-scope spike: there is no second tenant for a token to distinguish, and gating one
> local subprocess while every sibling subcommand stays open would be theater. The daemon still grew
> `/api/agent-runs` (create + list) for parity with every other domain's dual CLI+HTTP exposure (the
> web dashboard has no agent-runs screen yet, but the route exists and is gated by the same
> `AdminAuth` posture as `/api/repos` — no new scope). `gittrack`'s `context`/`pr`/`issues`
> subcommands are **not** included — those read the `context_bundle` domain, a separate wave, still
> not yet ported despite being listed alongside `whoami` in `gittrack`'s own six-subcommand binary.
> Go's `internal/store/agent_runs.go` stays in-tree unchanged; nothing is deleted until the final
> cleanup wave. `cargo test --workspace` 218 → 227 (+9: 3 in `gitstate-store`, 5 in `gitstate-daemon`,
> 1 in `gitstate-cli`).

---

## Product disciplines (unchanged by the transform)

**P1. Derived, not entered.** Dev work's source of truth is git — merged = done, PR open = in
progress. → We only claim "derived truth" where it's real; we never infer contribution from thin air.

**P2. Involvement, never a score.** Contribution is **texture across six dimensions** (shipped,
review, effort, quality, ownership, durability) — never a single rank, never a bonus formula. → Review
and ownership are counted so seniors/reviewers/maintainers aren't zeroed. The composite is displayed
as evidence-texture, never a leaderboard.

**P3. Estimates are evidence, not guesses.** Effort comes from an LLM reading the *shape* of the
change (difficulty 1–13), not lines or commits; a deterministic heuristic stands in when no LLM is
configured. → No story-point input field as the source of truth; every estimate links to its git
evidence.

**P4. Evidence with visible gaps.** What git can see is derived; what it can't (meetings, research) is
flagged for a human to fill, never auto-invented. → We under-count rather than fabricate.

**P5. Agent-native from day one.** Agent identities (Claude Code, Dependabot, …) are first-class:
every contribution carries an `agent_pct` and commits are split human/agent. → Survives the shift to
agent-written code; autonomous work is counted honestly, not hidden.

---

## Legacy SaaS architecture (A-series) — superseded, kept for provenance

These describe the pre-transform multi-tenant Go+Postgres stack still present in-tree under
`internal/`, `cmd/`, and `migrations/`. They are **superseded** by the T-series for the standalone
app; they remain accurate for the legacy code during the staged port.

- **A1. Go backend, single binary.** Strong concurrency for repo sync + LLM fan-out; web build
  embedded via `embed`. *(Superseded by T1/T2: the new core is Rust; the Go server is reference-only.)*
- **A2. Postgres (Neon) + RLS for tenancy.** `SET LOCAL app.current_org` inside each request tx.
  *(Superseded by T1: single-user local app has no tenancy; storage is SQLite.)*
- **A3. pgx + hand-written SQL.** Predictable, queryable, reviewable. *(Legacy only; Rust uses rusqlite.)*
- **A4. Forward-only migrations.** `YYYYMMDD_NNN_name.sql`, no up/down, checksums. *(The Rust store
  keeps forward-only migrations, but under `crates/gitstate-store/migrations/` — T11.)*
- **A5. JWT access + rotating refresh tokens.** *(Superseded by T1: no auth in a single-user local app.)*
- **A6. OAuth config-gated.** *(Superseded by T3: forge access uses the user's own `gh`/`glab`.)*
- **A7. Open core + EE (GitLab model), AGPL core.** *(Superseded by T10: MIT OR Apache-2.0, no EE.)*
- **A8. Bill USD, charge ZAR (Paystack).** *(Superseded by T12: no billing service.)*
- **A9. Server-rendered super-admin HTML.** *(Superseded by T1: no cross-org admin in a local app.)*
- **A10–A12. Shared root env / config file+env / fly.io deploy.** *(Superseded by T1: the app resolves
  a local data dir and needs no deploy target; the daemon binds `127.0.0.1` by default.)*
