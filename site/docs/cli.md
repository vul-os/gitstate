# CLI reference

The `gitstate` binary wires the same state the daemon uses. Machine-readable output is available with
`--json` where applicable.

```
gitstate <command>
```

**Global flags**

| Flag | Env | Meaning |
|---|---|---|
| `--data-dir <path>` | `GITSTATE_DATA_DIR` | Where the SQLite database lives. |
| `--json` | — | Machine output where applicable. |

---

## serve

Start the daemon — the headless always-on peer that serves the web UI and JSON API.

```
gitstate serve [--addr <ip>] [--port <n>] [--web-dist <path>]
```

| Flag | Env | Default |
|---|---|---|
| `--addr` | `GITSTATE_ADDR` | `127.0.0.1` |
| `--port` | `GITSTATE_PORT` | `7473` |
| `--web-dist` | — | bundled `../web/dist` if present |

---

## repo

```
gitstate repo add <path|remote_url>     # register a repo
gitstate repo list                      # table of repos (--json)
gitstate repo rm <id>
gitstate repo scan <id|--all>           # walk git (+ forge unless --no-forge)
    --no-forge                          # skip gh/glab — fully offline
    --since <rfc3339>                   # only history/items since a time
```

A local-path repo added without a remote is a `Local` forge and never touches the network.

---

## state

```
gitstate state <repo_id> [--json]
```

Prints the derived `ProjectState` — DORA cycle time, PR/issue flow, in-progress/done, change-failure.

---

## contributions / contributors

```
gitstate contributions <repo_id> [--from <rfc3339>] [--to <rfc3339>] [--json]
    --weights shipped=..,review=..,effort=..,quality=..,ownership=..,durability=..

gitstate contributors [--json]          # merged identities
```

`contributions` prints the six-dimension texture per contributor across the window. `--weights` only
tunes the display `composite`; the dimensions themselves are unweighted evidence.

---

## classify / effort

```
gitstate classify <repo_id> [--items <ref,ref>] [--json]   # default: all uncategorized
gitstate effort   <repo_id> [--items <ref,ref>] [--json]
```

See [Classification & effort](classification.md).

---

## context

```
gitstate context list
gitstate context show <id>
gitstate context create --name <n> [--desc <d>] [--repo <id>…] [--pr <slug#num>…] [--tag <t>…] [--notes <s>]
gitstate context edit <id> [--add-repo/--rm-repo <id>] [--add-tag/--rm-tag <t>] [--name …] [--notes …]
gitstate context rm <id>
gitstate context export <id> --out <file.json>    # portable working set
gitstate context import <file.json>
```

See [Contexts & P2P sync](contexts-sync.md).

---

## category / taxonomy

```
gitstate category list
gitstate category add --key <k> --label <l> [--parent <k>] [--color <#hex>]
gitstate category rm <id>

gitstate taxonomy show [--json]
gitstate taxonomy verify [--file <path>]          # verify signature against the pinned key
```

See [Signed taxonomy](taxonomy.md).

---

## sync / data

```
gitstate sync status
gitstate sync publish [--since <hlc>]             # only meaningful with --features sync-dmtap
gitstate data path                                # print resolved data dir + db path
```

`sync publish` is a no-op unless the binary was built with the optional `sync-dmtap` feature — see
[Contexts & P2P sync](contexts-sync.md).

Next: [HTTP API](api.md) · [Configuration](configuration.md)
