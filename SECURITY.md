# Security Policy

## Reporting a vulnerability

Please report security issues privately to the maintainers rather than opening a public issue.
Include a description, reproduction steps, and impact. We aim to acknowledge within a few business
days.

## Security model (local-first)

gitstate is a **standalone, local-first** application: a Rust core over a local SQLite database,
wrapped in a Tauri desktop app or run as a headless daemon. There is **no multi-tenant server, no
hosted account, and no cloud data store** — so the SaaS-era concerns (tenant isolation, session
tokens, payment webhooks, cross-org admin) no longer apply to the shipped product. The full model and
the residual hardening checklist live in [docs/security.md](docs/security.md). In summary:

- **No default network calls.** A scan of a local repository touches only your disk. Network access
  happens **only** for actions you initiate: reading your forge, or classifying against an LLM
  endpoint you configured. A plain `cargo build` never even compiles the P2P/sync stack (it is an
  excluded, opt-in crate).

- **Your credentials, your machine.** Forge access uses your own `gh`/`glab` login (or a token you
  place in the environment). gitstate stores no OAuth apps and brokers no tokens. Your LLM key is
  read from the environment and used only against the endpoint you set.

- **Code never leaves the box.** gitstate stores **aggregates**, not source: commit summaries (first
  line only), diff *shapes* (counts, languages, touched paths), and derived metrics — never file
  contents. What syncs peer-to-peer is limited to **contexts** (saved working sets) and
  **categories**; your commits, diffs, and contribution data are local and are never published.

- **Signed taxonomy, fail-closed.** The shared category taxonomy is an ed25519-signed data file
  verified against a pinned public key. On any hash/key/signature mismatch, gitstate refuses to serve
  taxonomy-sourced categories and falls back to local-only categories — it never silently trusts an
  unverified taxonomy.

- **P2P is content-blind, hub-less, and reaches nobody until you say so.** CRDT sync carries only
  context/category ops. There is no discovery: a peer exists because an operator typed its URL and its
  ed25519 public key, and an empty peer list means this node replicates with nobody. Every op is
  individually signed and verified on its own — a relayed change is not trusted for having arrived over
  an authenticated connection — and each request carries a single-use signed token. Every
  authentication failure is one `401` with no detail. See
  [docs/P2P-CONTEXTS.md](docs/P2P-CONTEXTS.md#authentication).

- **A node exposed on the internet fails closed at startup.** The daemon refuses to bind a
  non-loopback address while the management API has no authentication, rather than serving it and
  hoping. See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md). gitstate does not terminate TLS; that is
  documented rather than implied.

## Scope note

The legacy multi-tenant Go server (RLS tenancy, JWT auth, Paystack webhooks) remains **in-tree** under
`internal/`, `cmd/`, and `migrations/` for a staged port, but is **not** part of the standalone app's
runtime. Its security properties are documented for provenance in [docs/security.md](docs/security.md)
and are being retired domain-by-domain as the Rust port lands.
