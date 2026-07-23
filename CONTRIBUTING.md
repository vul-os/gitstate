# Contributing to gitstate

gitstate is a **standalone, local-first, peer-to-peer** desktop app — a Rust core over SQLite, a Tauri
shell, a headless daemon, and a React UI. It is being transformed from a legacy Go+Postgres SaaS; the
old Go server stays in-tree for a staged port (see [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md)).

## Dev setup

Prerequisites: **Rust** stable (1.85+), **Node** 20+, and — for forge features — `gh` and/or `glab`
logged in (or a token in the environment). A local LLM endpoint is optional.

```bash
git clone https://github.com/vul-os/gitstate
cd gitstate

# Core library + CLI + headless daemon (no P2P deps)
cargo build --workspace
cargo run -p gitstate-cli -- --help
cargo run -p gitstate-cli -- serve          # daemon on 127.0.0.1:7473

# Desktop app (boots the daemon, loads the React UI)
cd apps/desktop && npm install && npm run tauri dev

# Web UI standalone (point it at a locally-running daemon)
cd web && npm install && npm run dev
```

## Layout

```
crates/
  gitstate-core       pure domain: types + traits + derivation helpers, no I/O
  gitstate-git        git2-rs: walk, diff, blame survival, SZZ, contribution math
  gitstate-forge      GitHub + GitLab via gh/glab (+ REST/GraphQL token fallback)
  gitstate-classify   local LLM + signed taxonomy + heuristic + local personalization
  gitstate-store      rusqlite persistence (contexts, categories, caches, CRDT op log)
  gitstate-daemon     axum: serves web/dist + the JSON API (the headless peer)
  gitstate-cli        clap CLI (bin: gitstate)
  gitstate-sync       P2P CRDT sync — EXCLUDED from the workspace, feature `sync-dmtap`
apps/desktop          Tauri shell; frontendDist -> ../../web/dist
web/                  React + Vite UI, repointed at the daemon JSON API
docs/                 architecture, getting-started, classification, p2p, forge, migration notes
internal/ cmd/        KEPT legacy Go server (staged port — do NOT edit)
migrations/           KEPT legacy Go Postgres migrations (Rust migrations live in the store crate)
```

## Conventions

- **The domain crate is the contract.** `gitstate-core` owns the types and the four traits
  (`ForgeClient`, `Classifier`, `Store`, `SyncEngine`). Integration crates and the web client treat
  it as a read-only interface; don't diverge field names or shapes — extend by following the nearest
  existing pattern and note the addition.
- **Keep the local build network-free.** `cargo build --workspace` must not pull P2P/sync deps —
  `gitstate-sync` is excluded and behind the `sync-dmtap` feature. A local repo scan must make zero
  network calls.
- **One API surface.** The daemon serves both the desktop shell and headless mode. No domain logic
  crosses the Tauri IPC boundary — data flows over HTTP to the daemon in both modes. All web calls go
  through `web/src/lib/api.js`; no component calls `fetch` directly.
- **Do NOT edit the legacy Go code** under `internal/`, `cmd/`, or `migrations/`, nor `go.mod`/`go.sum`.
  It is kept verbatim as the reference for the staged port; a Go domain is removed only once its Rust
  replacement passes parity, in a dedicated commit.
- **Store aggregates, not source.** Commit summaries are first-line only; diffs are summarized as
  shapes (counts, languages, paths). Never persist or transmit file contents.
- **Fail closed on the taxonomy.** Signature/hash/pinned-key mismatches must fall back to local-only
  categories — never trust an unverified taxonomy.

## The wedge (keep it honest)

Changes must respect the product disciplines in [decisions.md](decisions.md): *derived-not-entered*,
*involvement-as-texture* (never a single score or bonus formula), *evidence with visible gaps*,
*agent-native*, and — since the transform — *local over hosted*. If a change would force a human to
invent a number, or would move a user's code/data off their machine, open an issue to discuss first.

## Building &amp; testing

```bash
cargo build --workspace          # must NOT pull the sync/P2P stack
cargo build -p gitstate-sync --features sync-dmtap   # the excluded crate, explicitly
cargo test --workspace
cargo fmt --all && cargo clippy --workspace

cd web && npm run build          # the daemon serves the built dist/
```

## License

By contributing, you agree your contributions are licensed under **MIT OR Apache-2.0** (see
[LICENSE-MIT](LICENSE-MIT) and [LICENSE-APACHE](LICENSE-APACHE)).
