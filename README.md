<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img src="assets/logo.svg" alt="gitstate" height="72">
</picture>

# gitstate

### Your git already knows. Stop typing it twice.

gitstate reads your repositories and **derives** project state, effort, contribution and
classification straight from git and your forge. No tickets to groom, no story points to invent, no
stand-up to reconstruct. It runs **on your machine**: a Rust core over one SQLite file, wrapped in a
Tauri desktop app — no SaaS backend, no multi-tenant server, no account. What you run is what you own.

<br>

<!-- Plain-text badges on purpose: rendering this README triggers no external
     image fetches — the same no-default-network-calls ethos as the app. -->
<sub>
<a href="LICENSE-MIT">MIT</a> OR <a href="LICENSE-APACHE">Apache-2.0</a>
&nbsp;·&nbsp; Rust 1.85+
&nbsp;·&nbsp; Tauri 2
&nbsp;·&nbsp; React + Vite
&nbsp;·&nbsp; SQLite
&nbsp;·&nbsp; local-first
&nbsp;·&nbsp; P2P (CRDT)
&nbsp;·&nbsp; no cloud
</sub>

<br><br>

[Quick start](#quick-start) ·
[What it is](#what-is-gitstate) ·
[How it works](#how-it-works) ·
[Screenshots](#screenshots) ·
[Architecture](#architecture) ·
[Classification &amp; decentralization](#classification--decentralization) ·
[Docs](docs/) ·
[Roadmap](ROADMAP.md)

<br>

<img src="docs/screenshots/dashboard.png" alt="gitstate desktop app dashboard — commit, merged-PR, cycle-time and contributor stat cards, a cycle-time trend chart, a contribution heatmap and a top-contributor list, all derived from git" width="900">

</div>

---

## What is gitstate?

Every project-tracking tool — Jira, Linear, ClickUp, ZenHub — is a **manually-maintained fiction**
sitting next to git. People re-type into tickets what they already did in the repo. The result is
unreliable *by construction*: estimates are off ~30% (and have been for 40 years), velocity is gamed
the moment it's a target, and timesheets are reconstructed from memory on Friday.

**Git is the real ledger.** gitstate stops asking humans to invent numbers — it observes work from
git and the forge — and makes whatever fiction remains *explicit*.

The difference from the old gitstate (and from every incumbent): **there is no central server.** A
Rust core over a plain SQLite file, wrapped in a Tauri desktop app. No account, no cloud, no
telemetry. It talks to GitHub/GitLab from **your** machine using **your** `gh`/`glab` login,
classifies with **your** local LLM (or a deterministic fallback), stores everything in a local
database, and shares saved working sets and categories **peer-to-peer** — never through a hub.

### Three disciplines constrain everything

> If a feature would force a human to invent a number, it doesn't ship.

| Discipline | What it means |
|---|---|
| **Derived, not entered** | State comes from git — merged = done, PR open = in progress. Nobody maintains tickets. |
| **Measure work, not workers** | Contribution is shown as *texture across six dimensions* (including review), never a single rank, never a bonus formula. |
| **Evidence-based, gaps visible** | Effort comes from an LLM reading the *shape* of a change (difficulty, not line count); what git can't see is flagged, never invented. |

### The six dimensions

Contribution is derived as six normalized dimensions — the gitstate essence — computed within the
repo cohort so seniors, reviewers, and maintainers are never zeroed:

| Dimension | Derived from |
|---|---|
| **Shipped** | merged PRs, closed issues |
| **Review** | reviews performed on others' work |
| **Effort** | Σ judged diff-difficulty (LLM reads the change; falls back to a deterministic heuristic) |
| **Quality** | inverted from reverts caused and SZZ-linked bug introductions (the model also supports a cycle-time factor, not yet fed per contributor) |
| **Ownership** | areas of the codebase authored/maintained |
| **Durability** | surviving lines ÷ authored lines (git blame) |

The composite is a weighted mean, and **you own the weights** — a live tuner in the UI, `PUT
/api/weights`, or `gitstate contributions --weights shipped=2,review=1,…`. That is the argument, not a
convenience: any ordering of people is an artefact of the weights someone chose, so gitstate hands
them over rather than shipping a leaderboard and calling its ranking objective.

Agent identities (Claude Code, Dependabot, …) are **first-class**: every contribution carries an
`agent_pct`, so autonomous work is counted honestly rather than hidden.

---

## How it works

Everything runs on your machine. The only network endpoints in the picture are ones **you**
configured — your forge (`gh`/`glab` or a token), optionally your local LLM, and optionally a tracker
you import from. A plain scan of a local repo makes **zero** network calls.

```mermaid
%%{init: {'theme':'base','themeVariables':{'fontFamily':'ui-monospace, SFMono-Regular, Menlo, monospace','primaryColor':'transparent','primaryBorderColor':'#14b8a6','primaryTextColor':'#8f969e','lineColor':'#8a8f98','nodeBorder':'#5f8f8a','edgeLabelBackground':'transparent','clusterBorder':'#3f8f86','clusterBkg':'transparent'}}}%%
flowchart LR
    subgraph machine["your machine"]
        direction LR
        git["your git repos<br/>(git2-rs: walk, diff, blame, SZZ)"]
        forge["gh / glab CLI<br/>(PRs, issues, reviews)"]
        core["gitstate core + daemon<br/>(derive state · effort · contribution)"]
        db[("SQLite<br/>contexts · categories · caches")]
        llm["local LLM (llmux /<br/>OpenAI-compatible) — optional"]
        app["Tauri desktop app<br/>(React UI over the daemon)"]
        git --> core
        forge --> core
        core <--> db
        core <-.->|"classify (opt-in)"| llm
        app <-->|"HTTP :7473"| core
    end
```

The desktop app and the headless daemon serve the **same** JSON API — the Tauri shell just boots the
daemon in-process on a local port and points the React UI at it. Run it headless (`gitstate serve`)
as an always-on peer, or as a desktop app; same core either way.

Between machines there is no hub. The only things that cross the network are **saved contexts** (a
working set of repos, PRs, notes, tags) and **categories**, synced **peer-to-peer as CRDTs** — so two
peers converge with no authority in the middle. Your commits, diffs, and code never leave your box.

```mermaid
%%{init: {'theme':'base','themeVariables':{'fontFamily':'ui-monospace, SFMono-Regular, Menlo, monospace','primaryColor':'transparent','primaryBorderColor':'#14b8a6','primaryTextColor':'#8f969e','lineColor':'#8a8f98','nodeBorder':'#5f8f8a','edgeLabelBackground':'transparent','clusterBorder':'#3f8f86','clusterBkg':'transparent'}}}%%
flowchart TB
    a["Alice's node<br/>(desktop or headless peer)"]
    b["Ben's node"]
    c["Chris's node"]
    a <-->|"contexts + categories<br/>(CRDT ops, signed transport)"| b
    b <-->|"CRDT ops"| c
    a <-->|"CRDT ops"| c
```

---

## Screenshots

<table>
<tr>
<td width="50%"><img src="docs/screenshots/board.png" alt="Board: four derived columns — open, in progress, merged, done — where every card is a PR, issue or review"><br><sub><em>Board — read-only and derived; a column you can drag is a column that can lie</em></sub></td>
<td width="50%"><img src="docs/screenshots/eng-health.png" alt="Eng Health: cycle time, change-failure rate, merge frequency and deploy-proxy cards, a bus-factor ownership panel, review coverage and quality signals"><br><sub><em>Eng Health — DORA-flavoured metrics, bus factor and review coverage, each labelled with how it was derived</em></sub></td>
</tr>
<tr>
<td width="50%"><img src="docs/screenshots/insights.png" alt="Insights view: ten headline stat cards, a year-long contribution heatmap, commit-volume, lines-added, cycle-time and throughput trend charts, plus a contributor table"><br><sub><em>Insights — a year of delivery, derived rather than self-reported</em></sub></td>
<td width="50%"><img src="docs/screenshots/contribution.png" alt="Contribution: six-dimension table across every repo with a live weight tuner"><br><sub><em>Contribution — six gaming-resistant dimensions, with the weights in your hands</em></sub></td>
</tr>
<tr>
<td width="50%"><img src="docs/screenshots/classify.png" alt="Classify: work items labelled against the signed taxonomy with confidence, method and rationale, plus a difficulty score per item"><br><sub><em>Classify — labels and difficulty judged locally, every row showing its method and rationale</em></sub></td>
<td width="50%"><img src="docs/screenshots/import.png" alt="Import: Jira and Linear credential forms using your own personal API token, plus an offline export-file path"><br><sub><em>Import — Jira &amp; Linear with <strong>your</strong> token, from your machine; or fully offline from an export</em></sub></td>
</tr>
</table>

<sub>All shots are the real desktop app against a deterministic synthetic demo dataset (`gitstate seed --demo`) — a fake org, fake pseudonymous contributors, never real git/forge history. Each one is a single 1440×900 app window: gitstate is a shell, not a document, so the rail and the top bar stay put and only the content pane scrolls (screens whose second half matters get their own scrolled capture, e.g. <a href="docs/screenshots/insights-trends.png">insights-trends</a>). See <a href="docs/screenshots/">docs/screenshots/</a> for all 20 (including <a href="docs/screenshots/dashboard.png">Dashboard</a>, <a href="docs/screenshots/involvement.png">Involvement</a>, <a href="docs/screenshots/people.png">People</a>, <a href="docs/screenshots/contexts.png">Contexts</a>, <a href="docs/screenshots/taxonomy.png">Taxonomy</a> and <a href="docs/screenshots/dashboard-light.png">light-mode</a>), and <code>web/scripts/screenshots.mjs</code> to regenerate them.</sub>

### Importing from Jira or Linear

Both are **local-first, like everything else**. Jira and Linear each issue *personal*
API tokens, so gitstate calls their public HTTPS API **from your machine with your own
credential** — the same posture it already takes with your `gh`/`glab` login. There is
no broker, no OAuth callback, and no gitstate server in the path. The token lives in
your local SQLite file, is never returned by the API once saved (reads are redacted),
and is sent only to the vendor it belongs to.

If a token isn't an option — an air-gapped machine, a locked-down Jira Server/DC
instance, or you'd simply rather not store a credential — paste or drop a Jira/Linear
**CSV/JSON export** instead. That path performs no network requests at all.

Imported issues land as ordinary work items, so classification, effort judging and
every analytics rollup treat them exactly like native ones. Ids are derived from
`(source, key)`, so re-importing updates in place rather than accumulating duplicates.

---

## Quick start

> **Status: v0.1, built in the open.** The Rust core, the daemon, the CLI, the desktop shell and every
> screen above work today. Packaged installers are still to come, a few analytics domains are still
> being ported from the legacy Go implementation under `internal/` (kept in-tree as the reference for a
> staged port — see [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md)), and P2P sync stays behind its
> feature flag until the transport settles. [ROADMAP.md](ROADMAP.md) and [PROGRESS.md](PROGRESS.md) say
> which is which.

### Prerequisites

- **Rust** stable (1.85+) and **Node** 20+ (for the desktop/web build).
- **`gh`** and/or **`glab`** on your `PATH`, logged in (`gh auth login`) — or a forge token in the
  environment (`GITSTATE_GH_TOKEN` / `GH_TOKEN`, `GITSTATE_GLAB_TOKEN` / `GITLAB_TOKEN`). No forge
  login is needed to scan a purely local repo.
- *(Optional)* a local LLM endpoint for classification and effort judging
  (`VULOS_LLMUX_URL` or `OPENAI_BASE_URL`). With none configured, gitstate uses a deterministic
  heuristic — everything still works, offline.

### Build from source

```bash
git clone https://github.com/vul-os/gitstate
cd gitstate

# Core library + CLI + headless daemon (no P2P deps pulled in)
cargo build --workspace
cargo run -p gitstate-cli -- --help

# Desktop app (starts the daemon in-process, loads the React UI)
cd apps/desktop && npm install && npm run tauri dev
```

`cargo build` never touches the P2P sync crate — `gitstate-sync` is **excluded** from the default
workspace and lives behind an optional `sync-dmtap` feature, so a bare build has no network stack.

### See every screen in 30 seconds

```bash
gitstate seed --demo      # deterministic synthetic dataset — no repo required
gitstate serve            # http://127.0.0.1:7473
```

Derived rows from the demo dataset carry a visible `synthetic demo data` warning, so it can never be
mistaken for real history.

### Try it for real (CLI)

```bash
# Register a repo and derive its state (git only — no forge, no network)
gitstate repo add ~/code/my-project
gitstate repo scan my-project --no-forge
gitstate state my-project

# Pull PRs/issues/reviews via your gh/glab login, then derive contributions
gitstate repo scan my-project
gitstate contributions my-project         # the six-dimension texture table
gitstate classify my-project              # local LLM if configured, else heuristic

# Save a working set and share it P2P (sync built with --features sync-dmtap)
gitstate context create --name "Q3 refactor" --repo <repo_id> --pr vul-os/gitstate#42 --tag refactor
gitstate context list

# Run as an always-on headless peer serving the same API + UI
gitstate serve            # binds 127.0.0.1:7473 (GITSTATE_ADDR / GITSTATE_PORT)
gitstate data path        # where your local database lives
```

Commands that take a repo accept the full id, an unambiguous id prefix, or the slug — `gitstate repo
list` shows all three.

Full CLI reference: [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md). Forge setup (gh/glab and
tokens): [docs/FORGE-SETUP.md](docs/FORGE-SETUP.md).

---

## Architecture

A Rust Cargo workspace modeled on its sibling products (`slipscan`, `ofisi`): pure-domain crates in
the middle, I/O crates at the edges, one daemon that serves both the desktop shell and headless peers.

| Crate | Role |
|---|---|
| **gitstate-core** | Pure domain — types (`Repo`, `Commit`, `Contribution`, `ProjectState`, `Context`, `Category`, `Taxonomy`, `Analytics`, `EngHealth`, …) and the four traits (`ForgeClient`, `Classifier`, `Store`, `SyncEngine`). No I/O. |
| **gitstate-git** | git2-rs derivation engine — walk history, diff, blame survival, SZZ bug-intro, and the six-dimension contribution math. |
| **gitstate-forge** | GitHub + GitLab via shelling `gh`/`glab` (REST/GraphQL fallback with a token) — PRs, issues, reviews. |
| **gitstate-classify** | Classifier — local LLM (llmux / any OpenAI-compatible endpoint) + a signed taxonomy + local personalization, with a deterministic heuristic fallback. |
| **gitstate-tracker** | Jira + Linear import: their public APIs called from your machine with your own personal token, plus an offline CSV/JSON export parser. |
| **gitstate-store** | rusqlite persistence — contexts, categories, derived caches, the CRDT op log. |
| **gitstate-daemon** | axum HTTP server — serves `web/dist` (SPA) **and** the JSON API, including the analytics, health and involvement rollups. The headless always-on peer. |
| **gitstate-cli** | clap CLI (`serve`, `seed`, `repo`, `state`, `contributions`, `contributors`, `classify`, `effort`, `context`, `category`, `taxonomy`, `sync`, `data`). |
| **gitstate-sync** | P2P CRDT sync of contexts + categories. **Excluded from the default workspace**; behind an optional `sync-dmtap` feature so a plain `cargo build` never pulls P2P deps. |
| **apps/desktop** | Tauri shell. Boots the daemon on an ephemeral local port and loads the **same** React app the headless mode serves — the UI is not forked. |
| **web/** | The kept React frontend, repointed at the daemon's JSON API (§ [web API contract](docs/ARCHITECTURE.md)); the old multi-tenant auth/org/billing surfaces are removed. |

Full contract: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

---

## Classification &amp; decentralization

gitstate classifies work items (features, bugfixes, refactors, security, agent-authored changes, …)
and judges effort. Two decisions keep this honest **and** decentralized:

- **Personal categorization is local-only.** Classification runs against **your** LLM endpoint
  (llmux or any OpenAI-compatible URL) or a deterministic heuristic — never a gitstate-hosted model.
  Corrections you make train a **local personalization** store: each box learns its own conventions.
  Nothing about your work is pooled.

- **Label alignment travels as a signed data file, not a service.** So that peers agree on what
  `feature.api` or `bugfix` *means*, gitstate ships a **versioned, content-addressed, ed25519-signed
  taxonomy** — verified against a pinned key, **fail-closed** (a bad signature falls back to
  local-only categories, never silently trusts). It's data you can inspect, not an endpoint you call.
  See [docs/CLASSIFICATION-AND-TAXONOMY.md](docs/CLASSIFICATION-AND-TAXONOMY.md).

**What is deliberately NOT built.** Cross-population features — trending, "others tagged this",
"similar repos" — need a view of strangers you'll never meet, so they don't belong in a git tool that
runs on your machine. That surface is left as a **dormant, optional coordinator seam** and nothing
more. There is no anti-spam/sybil tier (a tax on that unbuilt discovery layer) and no pooled
fine-tuning (replaced by local personalization). The rule: *only "needs a view of strangers"
belongs to an optional coordinator; everything a git tool is for is local + P2P.* Rationale in
[decisions.md](decisions.md).

---

## Docs

- [Getting started](docs/GETTING-STARTED.md) — install, first scan, the full CLI.
- [Architecture](docs/ARCHITECTURE.md) — crates, the daemon API, the web contract, the SQLite schema.
- [Classification &amp; taxonomy](docs/CLASSIFICATION-AND-TAXONOMY.md) — local LLM, the signed taxonomy, personalization.
- [P2P contexts](docs/P2P-CONTEXTS.md) — saved working sets, the CRDT merge model, sharing.
- [Forge setup](docs/FORGE-SETUP.md) — `gh`/`glab` and token configuration.
- [Migration notes](docs/MIGRATION-NOTES.md) — why the legacy Go server is still in-tree, and the staged port.
- [Security model](docs/security.md) · [Roadmap](ROADMAP.md) · [Decisions](decisions.md) · [Changelog](CHANGELOG.md)

The full documentation site — including the derivation model, analytics, the HTTP API and the threat
model — lives in [`site/`](site/) and reads at
[vulos.org/products/gitstate](https://vulos.org/products/gitstate).

---

## License &amp; contributing

gitstate is licensed **MIT OR Apache-2.0** — at your option (see [LICENSE-MIT](LICENSE-MIT) and
[LICENSE-APACHE](LICENSE-APACHE)), matching every sibling in the vulos suite. The former AGPL-3.0
license and the `ee/` commercial Enterprise tier were **removed** in the transform to a standalone
local-first app (there is no multi-tenant service to fence off).

Want to hack on it? See [CONTRIBUTING.md](CONTRIBUTING.md). Found a vulnerability? See
[SECURITY.md](SECURITY.md).

<div align="center"><sub>Git is the real ledger. Stop typing it twice — and keep it on your own machine.</sub></div>

---

<p align="center">
  <a href="https://vulos.org"><img src="site/assets/vulos-logo.png" alt="vulos" height="20"></a><br>
  <sub><a href="https://vulos.org"><b>vulos</b></a> — open by design</sub>
</p>
