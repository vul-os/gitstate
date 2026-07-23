# Configuration

gitstate is configured entirely through environment variables and CLI flags — there is no config
service and no account. Everything has a sensible default; a bare run works with no configuration at
all (local scans, heuristic classification).

---

## Daemon

| Variable / flag | Default | Meaning |
|---|---|---|
| `GITSTATE_ADDR` / `--addr` | `127.0.0.1` | Bind address for `gitstate serve`. |
| `GITSTATE_PORT` / `--port` | `7473` | Bind port. |
| `--web-dist <path>` | bundled `../web/dist` | Static UI directory to serve. |

The desktop app ignores these — it binds an ephemeral port in-process and injects the origin into the
webview.

---

## Data

| Variable / flag | Default | Meaning |
|---|---|---|
| `GITSTATE_DATA_DIR` / `--data-dir` | platform data dir | Directory holding `gitstate.db` (WAL). |

Run `gitstate data path` to print the resolved directory and database path.

---

## Forge access

gitstate prefers your local `gh` / `glab` CLIs; a token is only used when the CLI is absent.

| Variable | Meaning |
|---|---|
| `GITSTATE_GH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` | GitHub token (checked in that order). |
| `GITSTATE_GLAB_TOKEN`, `GITLAB_TOKEN` | GitLab token. |

If a repo is local-only (no remote), or you pass `--no-forge`, no network call is made and no token is
needed. A missing CLI with no token yields a typed `forge_cli_missing` error rather than a silent
partial result.

---

## Classification (LLM)

| Variable | Meaning |
|---|---|
| `VULOS_LLMUX_URL` / `OPENAI_BASE_URL` | OpenAI-compatible base URL. If unset, the heuristic classifier is used. |
| `VULOS_LLMUX_API_KEY` / `OPENAI_API_KEY` | API key for the endpoint, if it requires one. |
| `GITSTATE_CLASSIFY_MODEL` | Model name to request. |

Point these at your own [llmux](https://vulos.org) instance or any local model. Classification and
effort prompts send only item metadata and diff *shape* — never source code.

---

## Taxonomy

| Variable | Meaning |
|---|---|
| `GITSTATE_TAXONOMY_PATH` | Load a taxonomy from a file instead of the embedded default. |
| `GITSTATE_TAXONOMY_PUBKEY` | Pinned ed25519 public key the taxonomy must be signed with. Falls back to the compiled-in `DEFAULT_TAXONOMY_PUBKEY`. |

A taxonomy that fails verification is rejected and gitstate falls back to local-only categories — it
never silently trusts an unverified tree. See [Signed taxonomy](taxonomy.md).

---

## Sync (optional)

Peer-to-peer transport is an opt-in build feature:

```bash
cargo build -p gitstate-cli --features sync-dmtap
```

Without it, `sync status` reports `enabled:false` and `sync publish` is a no-op — but contexts and
categories still work fully offline. See [Contexts & P2P sync](contexts-sync.md).

Next: [CLI reference](cli.md) · [Threat model](threat-model.md)
