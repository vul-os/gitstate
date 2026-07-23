# FAQ

### Is there a hosted version?

No. gitstate is local-first by design. It runs on your machine as a desktop app or a headless daemon.
There is no gitstate account, no org model, and no billing cloud. The previous multi-tenant SaaS was
deliberately removed.

### Do I need GitHub or GitLab access?

Only if you want forge data (PRs, issues, reviews). gitstate reads your forge through the `gh` / `glab`
CLIs you already have — or REST with a token if the CLI is absent. A **local-only** scan derives
history, contribution, and blame-based durability with **no network access at all**.

### Does gitstate send my code anywhere?

No. Only aggregates are stored (commit line counts and summaries), and classification/effort prompts
send item metadata and diff *shape* — counts, languages, paths, title/body — never file contents. Run
a local LLM and even that stays on your machine.

### What if I don't configure an LLM?

Classification and effort fall back to a **deterministic heuristic** — keyword/path rules that are
reproducible and fully offline. Everything else (state, contributions, contexts) never needed an LLM.

### Why six dimensions instead of one score?

Because a single number becomes a target and then a lie (Goodhart's Law). Contribution is shown as
*texture* across shipped, review, effort, quality, ownership, and durability — so reviewers and mentors
aren't zeroed and nobody is ranked. See [Derivation model](derivation.md).

### How is effort measured?

By **difficulty**, judged from the diff's shape — not by line count. A 500-line generated migration is
trivial; a 20-line concurrency fix is not. See [Classification & effort](classification.md).

### How do contexts sync without a server?

Contexts and categories are CRDT documents — last-writer-wins scalars, add-wins sets, tombstoned
deletes — that converge peer-to-peer. The op log lives in SQLite; actual P2P transport is an opt-in
build feature. Nothing is required to be online. See [Contexts & P2P sync](contexts-sync.md).

### Are agents (Claude Code, Dependabot, …) supported?

Yes — gitstate is agent-native. Agent identities are first-class, autonomous runs are counted as work,
and human vs. agent commit shares are shown separately rather than blended. There's even an `agent`
category in the default taxonomy.

### What license is it?

**MIT OR Apache-2.0** — the VulOS suite standard. The old AGPL-3.0 license and commercial EE tier were
dropped in the transform.

### Is it part of VulOS?

Yes, but it's fully standalone — it never requires VulOS infrastructure. When siblings like Ofisi or
slip/scan are around, it can share contexts with them peer-to-peer. No hub, no coupling.

### Where's my data stored?

In one SQLite file under your platform data directory. Run `gitstate data path` to see exactly where,
or override with `GITSTATE_DATA_DIR`.

Next: [Getting started](getting-started.md) · [Configuration](configuration.md)
