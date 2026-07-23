# Security model (local-first)

gitstate is a **standalone, local-first** application: a Rust core over a local SQLite database,
wrapped in a Tauri desktop app or run as a headless daemon. There is **no multi-tenant server, no
hosted account, and no cloud data store**. This reshapes the threat model entirely — the SaaS-era
boundaries (tenant isolation, session tokens, payment webhooks, cross-org admin) are gone, replaced by
a much smaller surface centered on *keeping your data on your machine*.

> The legacy Go server's security properties (RLS tenancy, JWT auth, Paystack webhook verification,
> at-rest token encryption) are documented at the end of this file for provenance. That server is
> **kept in-tree** for a staged port ([MIGRATION-NOTES.md](MIGRATION-NOTES.md)) but is **not** part of
> the standalone app's runtime.

---

## 1. No default network calls

A scan of a local repository touches only your disk (`gitstate repo scan <id> --no-forge` makes **zero**
network calls). Network access happens **only** for actions you explicitly initiate:

- Reading your forge (only when you scan with forge enabled), using **your** `gh`/`glab` login or a
  token you placed in the environment — see [FORGE-SETUP.md](FORGE-SETUP.md).
- Classifying against an **LLM endpoint you configured** (`VULOS_LLMUX_URL` / `OPENAI_BASE_URL`). With
  none set, classification uses a local deterministic heuristic and stays offline.
- Peer-to-peer sync of contexts/categories — and only if you built with the `sync-dmtap` feature. A
  plain `cargo build` doesn't even compile the P2P stack.

The daemon binds `127.0.0.1` by default.

## 2. Your credentials stay yours

gitstate registers no OAuth application and brokers no tokens. Forge access reuses the credentials
already on your machine (the `gh`/`glab` session, or a PAT you export). LLM keys are read from the
environment and used only against the endpoint you set. gitstate persists no forge or LLM secret to
its database.

## 3. Code never leaves the box

gitstate stores **aggregates, not source**:

- Commit records keep the **first line** of the message only, plus counts (additions, deletions,
  files) — never file contents or diffs.
- Effort judging operates on a `DiffSummary` (counts, languages, touched paths) — the *shape* of a
  change, not its text.
- Derived caches (project state, contributions, work items, classifications) are **local** and never
  synced.

What can be shared peer-to-peer is limited to **contexts** (saved working sets: repos, PR refs, notes,
tags) and **categories**. Your commits, diffs, and contribution data are never published.

## 4. Signed taxonomy, fail-closed

The shared category taxonomy is an ed25519-signed, content-addressed data file. `verify()` recomputes
the content hash, checks the **pinned** public key (`GITSTATE_TAXONOMY_PUBKEY` or the compiled-in
`DEFAULT_TAXONOMY_PUBKEY`), and verifies the signature. On any mismatch → `Error::TaxonomyUntrusted`,
and gitstate refuses to serve taxonomy-sourced categories, falling back to local-only categories. It
never silently trusts an unverified taxonomy. (Full detail: [CLASSIFICATION-AND-TAXONOMY.md](CLASSIFICATION-AND-TAXONOMY.md).)

> The taxonomy currently ships with a **development** signing key; production must re-sign with the
> offline release key ([decisions.md](../decisions.md) T5).

## 5. P2P is opt-in and hub-less

CRDT sync is a separate, **excluded** crate behind the `sync-dmtap` feature. It carries only
context/category ops over a signed transport on the shared vulos/DMTAP substrate — no central hub, no
code or metrics in the payload.

## 6. Local data at rest

Your database is a plain SQLite file under the resolved data directory (`gitstate data path`,
overridable with `GITSTATE_DATA_DIR`). It is protected by your operating system's file permissions and
whatever disk encryption you run; gitstate adds no separate encryption layer over it. Because it holds
only aggregates (not source), and lives solely on your machine, the blast radius of the file is your
own device. Back it up by copying the folder.

## Residual items for the standalone app

- [ ] **CORS tightening** — the daemon allows `localhost` origins; confirm no broader origin is
      accepted in any build.
- [ ] **Forge CLI argument hygiene** — ensure repo slugs/refs passed to `gh`/`glab` are validated so a
      crafted slug can't inject flags.
- [ ] **LLM endpoint egress** — document that a user-configured LLM URL receives work-item titles/bodies
      and diff shapes; keep it a conscious, configured choice.
- [ ] **Taxonomy release key** — replace the development signing key before any signed distribution.
- [ ] **Sync transport review** — a security pass on the `sync-dmtap` transport before it ships enabled.

---

## Appendix — legacy SaaS security (provenance only)

The pre-transform Go server, still in-tree under `internal/`/`cmd/`/`migrations/`, enforced:
multi-tenant isolation via PostgreSQL Row-Level Security (`SET LOCAL app.current_org` per request tx,
proven by `internal/store/rls_test.go` returning zero cross-org rows); audited super-admin access
(`audit_log`, never ambient); env-only secrets; Paystack webhook HMAC-SHA512 verification with
idempotency; per-IP rate limiting; and AES-256-GCM at-rest encryption of repo tokens. These properties
apply to the legacy code during the staged port and are being retired as each domain is ported to
Rust; they are **not** part of the local-first app described above.
