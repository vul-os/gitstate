# Threat model

gitstate is local-first specifically to shrink its attack surface. The old multi-tenant SaaS was a
Postgres honeypot holding every team's git activity behind a login. This one has no server side to
breach, subpoena, or shut down.

---

## What gitstate is

- A desktop app and a headless daemon that run **on your machine**.
- A single **SQLite** file of derived aggregates — commit line counts and summaries, work-item
  metadata, contexts, categories. **No source code is stored.**
- Outbound network access **only** to endpoints you explicitly configure: your forge (via `gh`/`glab`
  or a token) and, optionally, an LLM endpoint. A local-repo scan makes **zero** network calls.

## What gitstate is not

- Not a hosted service. There is no gitstate account, no org model, no billing cloud, no telemetry.
- Not a code exfiltrator. Classification and effort prompts send item metadata and diff *shape*
  (counts, languages, paths, title/body) — never file contents.

---

## Trust boundaries

| Boundary | Posture |
|---|---|
| **Daemon bind** | Defaults to `127.0.0.1`. Binding a public address is your explicit choice; there is no authentication layer, so don't expose it to an untrusted network. |
| **CORS** | Permissive for `localhost` origins only. |
| **Forge tokens** | Read from the environment, used only when the CLI is absent, never persisted to the database. |
| **Tracker tokens** | Jira/Linear personal tokens **are** persisted — they have no CLI to borrow from. They live in your local SQLite file, are returned only as a masked hint (`…9f2c`), and are sent only to the vendor they belong to. The offline export path lets you skip storing one entirely. |
| **LLM endpoint** | You choose it. Prefer a local model or your own llmux to keep even diff shape on-prem. |
| **Taxonomy** | ed25519-signed and verified against a pinned key. A bad signature fails **closed** to local-only categories — never silently trusted. |
| **P2P sync** | Reaches nobody until an operator enrols a peer by URL **and** ed25519 public key, out of band — there is no discovery and no default endpoint. Every op is individually signed and verified on its own; requests carry single-use signed tokens; the responder signs its replies. Not encrypted: put TLS in front of an internet-reachable node. |
| **Exposed node** | The daemon refuses to bind a non-loopback address while the management API has no authentication. |

---

## Peer-to-peer considerations

When sync is enabled, contexts and categories converge as CRDT ops with trusted peers. The ops carry
your working-set metadata (repo slugs, PR numbers, tags, notes) — treat a peer as you would anyone you
share a working set with. gitstate deliberately builds **no** cross-population/discovery layer, so
there is no mechanism by which strangers you never chose can reach your data.

---

## Data at rest

The database sits in your platform data directory. It is not encrypted at rest by gitstate — rely on
your OS disk encryption. Because only aggregates and metadata are stored (never source), the blast
radius of a lost laptop is far smaller than a cloud breach of a multi-tenant git-analytics service.
The one secret it can hold is a tracker token, which is why the import screen also offers a path that
stores none.

## API surface

The JSON API has **no authentication**, deliberately: the boundary is the loopback interface and your
OS user account, not a token gitstate would then have to store and rotate. Two consequences worth
stating plainly:

- Any process running as you can talk to the daemon while it is up. That is the same trust level as
  any process that can read your git repositories in the first place.
- `GITSTATE_ADDR` will happily bind `0.0.0.0`. Doing so puts an unauthenticated API on your network —
  if you need remote access, put it behind an SSH tunnel or a reverse proxy that does authentication.

---

## Reporting

Security reports go through the repository's `SECURITY.md`. Because there is no hosted infrastructure,
a vulnerability affects only locally-run software — there is no shared service to coordinate a fleet
patch around.

Next: [Signed taxonomy](taxonomy.md) · [Architecture](architecture.md)
