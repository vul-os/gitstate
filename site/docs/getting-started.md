# Getting started

gitstate is a **local-first desktop app** that derives true project state, effort, contribution, and
classification directly from your own git repositories and forge — on your machine. There is no
multi-tenant server, no Postgres, no account to create. The git history *is* the ledger.

You can run gitstate three ways, all from the same Rust workspace:

- **Desktop app** — a Tauri shell that starts the daemon in-process and loads the React UI.
- **Headless daemon** — `gitstate serve` runs an always-on peer that serves the same UI and JSON API.
- **CLI** — `gitstate <command>` for scans, state, contributions, classification, and contexts.

---

## Requirements

- A **Rust** toolchain (1.85+) — `rustup` is the easiest path.
- **Node** (for building the desktop shell's frontend; not needed for the CLI or headless daemon).
- Optionally the **`gh`** and/or **`glab`** CLIs, authenticated, so gitstate can read pull requests,
  issues, and reviews. Without them a **local-only** scan still works — it just skips forge data.
- Optionally an **OpenAI-compatible LLM endpoint** (your own [llmux](https://vulos.org), or any local
  model) for classification and effort judging. Without one, a deterministic heuristic is used.

Nothing else. No database server, no Docker, no cloud credentials.

---

## Build & run

```bash
git clone https://github.com/vul-os/gitstate
cd gitstate
cargo build --workspace
```

That builds the core, the CLI and the daemon. `cargo build --workspace --locked --offline` succeeds
with no network access at all, and gitstate depends on no other vulos product — `Cargo.lock` carries no
`git` sources. Peer replication is compiled in and stays inert until you enrol a peer.

### See every screen in 30 seconds

```bash
cargo run -p gitstate-cli -- seed --demo    # synthetic dataset, no repo needed
cargo run -p gitstate-cli -- serve          # http://127.0.0.1:7473
```

The demo dataset is a fake org with pseudonymous contributors; derived rows carry a visible
`synthetic demo data` warning so it can never be mistaken for real history.

### Point it at something real

```bash
# register a repo (worktree path or remote URL) and derive its state
cargo run -p gitstate-cli -- repo add ~/code/my-project
cargo run -p gitstate-cli -- repo scan my-project      # add --no-forge to stay offline
cargo run -p gitstate-cli -- state my-project

# or launch the desktop app (boots the daemon in-process)
cd apps/desktop && npm install && npm run tauri dev
```

Commands that take a repo accept the full id, an unambiguous id prefix, or the slug
(`demo-org/atlas-api`, or just `atlas-api` when that's unique) — `gitstate repo list` shows all three.

`repo scan` walks history with [git2](https://docs.rs/git2) and — unless you pass `--no-forge` — pulls
PRs, issues and reviews through `gh`/`glab`. Everything is cached in a single SQLite file under your
platform data directory (override with `--data-dir` or `GITSTATE_DATA_DIR`). Run `gitstate data path`
to see exactly where.

---

## First look

Once a repo is scanned:

- **Dashboard / Insights** — headline counts, the cycle-time trend, a year-long heatmap and the
  contributors behind it. See [Analytics & health](analytics.md).
- **Board** — open / in progress / merged / done, derived from the ledger and read-only on purpose.
- **Eng Health** — DORA-flavoured delivery metrics, bus factor, review coverage and quality proxies,
  each labelled with how it was derived.
- **Contribution / Involvement / People** — six-dimension contribution *texture* per contributor with
  the weights in your hands, who touches which repo, and identities merged from commit emails. See
  [Derivation model](derivation.md).
- **Classify / Taxonomy** — work items tagged against a signed taxonomy with effort judged from the
  diff. See [Classification & effort](classification.md).
- **Import** — pull Jira and Linear issues in with your own token, or from an export file offline. See
  [Jira & Linear import](import.md).
- **Contexts** — saved working sets of repos, PRs, tags and notes that you can keep private or share
  peer-to-peer. See [Contexts & P2P sync](contexts-sync.md).

---

## Turning on classification

Classification and effort judging use whatever OpenAI-compatible endpoint you point at:

```bash
export VULOS_LLMUX_URL="http://127.0.0.1:8080/v1"   # or OPENAI_BASE_URL
export GITSTATE_CLASSIFY_MODEL="your-model"          # optional
cargo run -p gitstate-cli -- classify my-project
```

Leave the endpoint unset and gitstate falls back to a deterministic keyword/path heuristic — offline,
reproducible, and always available. Details in [Configuration](configuration.md).

---

---

## Status

gitstate is **v0.1 and built in the open**. The Rust core, the daemon, the CLI, the desktop shell and
every screen in the [screenshot gallery](screenshots.md) work today. Packaged installers and a few
remaining ported analytics domains are still landing, and the P2P sync crate stays behind its feature
flag until the transport settles — the [roadmap](roadmap.md) says which is which.

Next: [Configuration](configuration.md) · [CLI reference](cli.md) · [Architecture](architecture.md)
