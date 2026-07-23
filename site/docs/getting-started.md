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

# register a repo (its worktree path or a remote URL) and derive its state
cargo run -p gitstate-cli -- repo add .
cargo run -p gitstate-cli -- repo scan --all
cargo run -p gitstate-cli -- state vul-os/gitstate

# run the always-on peer — serves the web UI and the JSON API on :7473
cargo run -p gitstate-cli -- serve

# or launch the desktop app
cd apps/desktop && npm install && npm run tauri dev
```

The first `repo scan` walks history with [git2](https://docs.rs/git2) and — unless you pass
`--no-forge` — pulls PRs, issues, and reviews through `gh`/`glab`. Everything is cached in a single
SQLite file under your platform data directory (override with `--data-dir` or `GITSTATE_DATA_DIR`).
Run `gitstate data path` to see exactly where.

---

## First look

Once a repo is scanned:

- **Project state** — DORA cycle time (first-commit → merge), change-failure rate, and
  in-progress/done counts, all derived. See [Derivation model](derivation.md).
- **Involvement** — six-dimension contribution *texture* per contributor, never a single rank. See
  [Derivation model](derivation.md).
- **Classify / Effort** — work items tagged against a signed taxonomy and effort judged from the diff.
  See [Classification & effort](classification.md).
- **Contexts** — saved working sets of repos, PRs, tags, and notes that you can keep private or share
  peer-to-peer. See [Contexts & P2P sync](contexts-sync.md).

---

## Turning on classification

Classification and effort judging use whatever OpenAI-compatible endpoint you point at:

```bash
export VULOS_LLMUX_URL="http://127.0.0.1:8080/v1"   # or OPENAI_BASE_URL
export GITSTATE_CLASSIFY_MODEL="your-model"          # optional
cargo run -p gitstate-cli -- classify vul-os/gitstate
```

Leave the endpoint unset and gitstate falls back to a deterministic keyword/path heuristic — offline,
reproducible, and always available. Details in [Configuration](configuration.md).

---

Next: [Architecture](architecture.md) · [CLI reference](cli.md) · [HTTP API](api.md)
