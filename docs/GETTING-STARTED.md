# Getting started

gitstate is a **standalone, local-first** tool. It runs on your machine, reads your git repositories
and your forge, derives project state and contribution, classifies work, and stores everything in a
local SQLite database. There is no account and no cloud.

> **Status: transform in progress.** The Rust workspace, the Tauri shell, and the repointed React UI
> are landing now (see [PROGRESS.md](../PROGRESS.md)). This guide describes the intended CLI and
> daemon surface per the [architecture contract](ARCHITECTURE.md); some commands may be partially
> wired while the crates are built out.

## Prerequisites

- **Rust** stable (1.85+) and **Node** 20+ (for the desktop/web build).
- **`gh`** and/or **`glab`** on your `PATH` and logged in — or a forge token in the environment. See
  [FORGE-SETUP.md](FORGE-SETUP.md). No forge login is needed to scan a purely local repo.
- *(Optional)* a local LLM endpoint for classification/effort (`VULOS_LLMUX_URL` or `OPENAI_BASE_URL`).
  With none set, gitstate uses a deterministic heuristic and stays fully offline.

## Build from source

```bash
git clone https://github.com/vul-os/gitstate
cd gitstate

# Core library + CLI + headless daemon (no P2P deps pulled in)
cargo build --workspace

# Desktop app (starts the daemon in-process, loads the React UI)
cd apps/desktop && npm install && npm run tauri dev
```

`cargo build` never touches the P2P sync crate — `gitstate-sync` is excluded from the default
workspace and lives behind an optional `sync-dmtap` feature.

## Where your data lives

```bash
gitstate data path
```

gitstate resolves an OS-appropriate data directory (overridable with `GITSTATE_DATA_DIR` or the global
`--data-dir` flag) and keeps a single SQLite database (`gitstate.db`, WAL mode) there — repos,
contributors, derived caches, contexts, categories, and the CRDT op log. Back it up by copying that
folder; move it with `--data-dir`.

## Your first scan (local only, no network)

```bash
gitstate repo add ~/code/my-project      # register a worktree
gitstate repo list                       # see the repo id
gitstate repo scan <repo_id> --no-forge  # walk git only — zero network calls
gitstate state <repo_id>                 # print the derived ProjectState
gitstate contributions <repo_id>         # the six-dimension texture table
```

## Adding forge data

With `gh`/`glab` logged in (or a token set), drop `--no-forge` to pull PRs, issues, and reviews:

```bash
gitstate repo scan <repo_id>                       # git + forge
gitstate repo scan <repo_id> --since 2026-01-01T00:00:00Z
gitstate contributors                              # merged identities
gitstate contributions <repo_id> --from 2026-01-01T00:00:00Z --to 2026-07-01T00:00:00Z \
  --weights shipped=1,review=1,effort=1,quality=1,ownership=1,durability=1
```

## Classification &amp; effort

```bash
gitstate classify <repo_id>        # local LLM if configured, else heuristic
gitstate effort <repo_id>          # judged diff-difficulty per work item
```

Corrections are learned locally and re-rank future suggestions — see
[CLASSIFICATION-AND-TAXONOMY.md](CLASSIFICATION-AND-TAXONOMY.md).

## Saved working sets (contexts)

A **context** is a saved working set — repos, PR references, notes, tags — and it is the unit gitstate
shares peer-to-peer:

```bash
gitstate context create --name "Q3 refactor" --repo <repo_id> \
  --pr vul-os/gitstate#42 --tag refactor --notes "cross-service cleanup"
gitstate context list
gitstate context export <id> --out q3.json     # portable, shareable out-of-band
gitstate context import q3.json
```

See [P2P-CONTEXTS.md](P2P-CONTEXTS.md) for the CRDT sync model.

## Running as a headless peer

```bash
gitstate serve                     # binds 127.0.0.1:7473 by default
GITSTATE_PORT=8080 gitstate serve  # or --addr / --port
```

The daemon serves the same JSON API and the React UI (`web/dist`) that the desktop app uses — run it
on an always-on box to keep a peer online. Global flags: `--data-dir <path>` and `--json` (machine
output where applicable).

## CLI overview

```
gitstate serve                      # headless daemon peer
gitstate repo add|list|rm|scan      # manage repos, derive from git (+forge)
gitstate state <repo_id>            # ProjectState
gitstate contributions <repo_id>    # six-dimension contribution table
gitstate contributors               # merged identities
gitstate classify <repo_id>         # classify work items
gitstate effort <repo_id>           # judge diff-difficulty
gitstate context list|show|create|edit|rm|export|import
gitstate category list|add|rm
gitstate taxonomy show|verify       # inspect / verify the signed taxonomy
gitstate sync status|publish        # meaningful only with --features sync-dmtap
gitstate data path                  # resolved data dir + db path
```
