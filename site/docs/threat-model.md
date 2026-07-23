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
| **LLM endpoint** | You choose it. Prefer a local model or your own llmux to keep even diff shape on-prem. |
| **Taxonomy** | ed25519-signed and verified against a pinned key. A bad signature fails **closed** to local-only categories — never silently trusted. |
| **P2P sync** | Optional, off by default, and not even compiled unless you build `--features sync-dmtap`. |

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

---

## Reporting

Security reports go through the repository's `SECURITY.md`. Because there is no hosted infrastructure,
a vulnerability affects only locally-run software — there is no shared service to coordinate a fleet
patch around.

Next: [Signed taxonomy](taxonomy.md) · [Architecture](architecture.md)
