# Screenshots

Every image below is a **real capture** of the running app — no mockups. The desktop shell and the
headless daemon render the *same* React UI over the *same* JSON API, so a `gitstate serve` in a
browser looks exactly like this too.

They are captured against `gitstate seed --demo`: a deterministic synthetic dataset with a fake org
and pseudonymous contributors, never anyone's real git or forge history. Regenerate the whole set with
`npm run shots` from `web/` (Playwright, 1440×900 at 2×, dark and light).

Every shot is one app window at the same size — gitstate is a shell, not a document: the rail and the
top bar stay put and only the content pane scrolls. Where the interesting half of a screen sits below
the fold, there is a second capture with that pane scrolled, rather than one stretched page-height
ribbon.

---

## Dashboard

![gitstate dashboard: commit, merged-PR, cycle-time and contributor stat cards, a cycle-time trend chart, a contribution heatmap and a top-contributor list](screenshots/dashboard.png)

Your local project ledger — headline counts, the cycle-time trend per merged PR, a year-long
contribution heatmap and the contributors behind it. Every number comes from a commit, not a status
field.

## Board

![A four-column board — open, in progress, merged, done — where every card is a PR, issue or review derived from git](screenshots/board.png)

Read-only and derived. There is nothing to create, assign or drag: each card is a pull request, issue
or review gitstate already parsed. A column you can drag is a column that can lie.

## Eng Health

![Eng Health: cycle time, change-failure rate, merge frequency and deploy-proxy cards over bus factor, review coverage and quality signals](screenshots/eng-health.png)

Delivery health, ownership risk, review coverage and quality proxies — with the proxies labelled as
proxies. See [Analytics & health](analytics.md) for exactly how each one is derived.

![Eng Health, scrolled: review coverage beside the quality-signals row — test-touch, average commit size, large-commit share and reverts](screenshots/eng-health-quality.png)

## Contribution

![Contribution: the live weight tuner over the six dimensions](screenshots/contribution.png)

The six gaming-resistant dimensions across every repo, with the weights in your hands. Drag a slider
and the table re-ranks — which is the point: any ordering is an artefact of the weights someone chose.

![Contribution, scrolled: the six-dimension table per contributor with agent share](screenshots/contribution-table.png)

## Insights

![Insights: ten headline stat cards and a year-long contribution heatmap](screenshots/insights.png)

A year of delivery in one screen — volume, lines changed, cycle time and throughput, all derived.

![Insights, scrolled: commit-volume, lines-added, cycle-time and throughput charts over the contributor table](screenshots/insights-trends.png)

## Involvement

![Involvement: which repos each person touches and which people carry each repo, derived from commit authorship](screenshots/involvement.png)

Who touches what, read from both directions — from commit authorship, not from an org chart anyone
maintains.

## People

![People: contributor identities, each grouping a username with its linked email addresses](screenshots/people.png)

Identities merged automatically by the emails seen across git history, with each `@username` shown
together with the addresses linked to it. Agent identities are first-class, never blended into a
human's row.

## Classify

![Classify: work items labelled with category, confidence, method and rationale, each with a difficulty score and a correction dropdown](screenshots/classify.png)

Labels and diff-difficulty judged locally — via your own llmux or OpenAI-compatible endpoint, or the
deterministic heuristic. Every row shows the method that produced it, and corrections train this
machine only. See [Classification & effort](classification.md).

## Taxonomy

![Taxonomy: the signed category tree with its version, content hash and verification status](screenshots/taxonomy.png)

The shared vocabulary, shipped as a signed data file rather than served by anyone. See
[Signed taxonomy](taxonomy.md).

## Contexts

![Contexts: saved working sets of repos, pull requests, tags and notes](screenshots/contexts.png)

Saved working sets — the unit gitstate shares peer-to-peer over CRDT. See
[Contexts & P2P sync](contexts-sync.md).

## Import

![Import: Jira and Linear token forms plus an offline export-file path](screenshots/import.png)

Jira and Linear with **your** personal token, called from your machine — or fully offline from a
CSV/JSON export. See [Jira & Linear import](import.md).

## Repos

![Repos: registered repositories with their forge, default branch and last scan](screenshots/repos.png)

Register git worktrees and forge remotes. Registration is local: nothing is announced anywhere.

## Light mode

![The gitstate dashboard rendered in the warm-paper light theme](screenshots/dashboard-light.png)

The same screens in a warm-paper light theme, contrast-checked against the same tokens. The capture
pipeline shoots both themes, so neither one quietly rots.

---

## CLI & headless daemon

`gitstate serve` is an always-on peer for servers and scripts — the same API the desktop app talks to,
no window required.

```
$ gitstate state 5d6fe96b
repo         5d6fe96b-8686-9274-0165-97fbab4325e4
head         d42d4868a7691cfdbfbfdb0f32664f85c5a066ad
prs          open=4 merged=40 draft=2
issues       open=7 closed=21
flow         in_progress=4 done=61
cycle time   p50=8.0 p90=17.6 (hours)
change fail  0.2

$ gitstate serve
gitstate serve: http://127.0.0.1:7473
```

Full command list: [CLI reference](cli.md).
