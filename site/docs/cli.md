# CLI reference

The `gitstate` binary wires the same state the daemon uses. Machine-readable output is available with
`--json` where applicable.

```bash
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

```bash
gitstate serve [--addr <ip>] [--port <n>] [--web-dist <path>]
```

| Flag | Env | Default |
|---|---|---|
| `--addr` | `GITSTATE_ADDR` | `127.0.0.1` |
| `--port` | `GITSTATE_PORT` | `7473` |
| `--web-dist` | — | bundled `../web/dist` if present |

---

## seed

Populate a database with the deterministic synthetic demo dataset — a fake org, fake pseudonymous
people, never real git or forge history. It is the fastest way to see every screen with data, and it
is what the [screenshots](screenshots.md) are captured against.

```bash
gitstate seed --demo [--db <file>]
```

By default it writes the same `gitstate.db` that `gitstate serve` opens, so
`gitstate seed --demo && gitstate serve` needs no extra flags. Derived rows carry a visible
`synthetic demo data` warning so a demo database can never be mistaken for a real one.

---

## repo

```bash
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

```bash
gitstate state <repo_id> [--json]
```

Prints the derived `ProjectState` — DORA cycle time, PR/issue flow, in-progress/done, change-failure.

```
repo         5d6fe96b-8686-9274-0165-97fbab4325e4
head         d42d4868a7691cfdbfbfdb0f32664f85c5a066ad
prs          open=4 merged=40 draft=2
issues       open=7 closed=21
flow         in_progress=4 done=61
cycle time   p50=8.0 p90=17.6 (hours)
change fail  0.2
```

---

## contributions / contributors

```bash
gitstate contributions <repo_id> [--from <rfc3339>] [--to <rfc3339>] [--json]
    --weights shipped=..,review=..,effort=..,quality=..,ownership=..,durability=..

gitstate contributors [--json]          # merged identities
```

`contributions` prints the six-dimension texture per contributor across the window, resolving each
contributor id to its merged display name (agents are marked). `--weights` persists the composite
weights and tunes the `comp` column; the dimensions themselves are unweighted evidence.

```
contributor                      ship    rev    eff   qual    own    dur    comp agent%
Nour Haddad                        73     33     59     86     85     35    61.8     0%
Wei Zhang                          83     48     36     77     41     70    59.2     0%
Refactor Agent [agent]             49     89     87     69     25     32    58.5    95%
```

---

## classify / effort

```bash
gitstate classify <repo_id> [--items <ref,ref>] [--json]   # default: all uncategorized
gitstate effort   <repo_id> [--items <ref,ref>] [--json]
```

See [Classification & effort](classification.md).

---

## context

```bash
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

```bash
gitstate category list
gitstate category add --key <k> --label <l> [--parent <k>] [--color <#hex>]
gitstate category rm <id>

gitstate taxonomy show [--json]
gitstate taxonomy verify [--file <path>]          # verify signature against the pinned key
```

See [Signed taxonomy](taxonomy.md).

---

## sync / data

```bash
gitstate sync status                              # peer id, how many peers are enrolled
gitstate sync identity                            # this node's peer id + public key
gitstate sync peer add --id <id> --url <url> --key <hex> [--label <name>]
gitstate sync peer list
gitstate sync peer remove --id <id>
gitstate sync run                                 # one round with every enrolled peer
gitstate sync publish [--since <hlc>]             # record local ops in the op log (no network)
gitstate data path                                # print resolved data dir + db path
```

Enrolment is manual in both directions: each operator runs `sync identity` and hands the peer id and
public key to the other **out of band**, along with the node's URL. There is no discovery, so with no
peers enrolled `sync run` reaches nobody and says so. See
[Contexts & P2P sync](contexts-sync.md) and [Deployment](deployment.md).

Next: [HTTP API](api.md) · [Configuration](configuration.md)
