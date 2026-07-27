# Analytics & engineering health

Everything on the Dashboard, Insights, Eng Health and Involvement screens is computed from the rows
already in your local database — commits, pull requests, issues and reviews. No screen asks anyone to
fill in a field, and none of them phone anywhere: the analytics endpoints read SQLite and return.

---

## One window, four endpoints

`/api/analytics`, `/api/health-metrics`, `/api/involvement` and `/api/contributions/rollup` resolve
their date range **identically**, so two screens never disagree about "the last 90 days":

- `from`/`to` — explicit RFC3339 bounds win over everything else.
- `days` — a trailing window (default **180**), ignored when `from` is given.
- The window anchors on the **newest commit in the store**, not on wall-clock now.

That anchoring is deliberate. A repo you last scanned in March still renders a populated view in June
instead of a set of empty charts, and a demo database behaves the same as a live one.

`repo_id` narrows any of them to a single repo; omit it and they span every registered repo.

---

## What the dashboard and insights show

`GET /api/analytics` is a single round-trip that returns everything both screens need:

| Field | What it is |
|---|---|
| `totals` | Headline scalars — commits, repos, contributors, additions/deletions/net lines, active days, merge commits, test-touch commits and rate, open/merged PRs, open/closed issues, commits per active day, lines per commit, cycle p50/p90. |
| `heatmap` | One bucket per day: `date`, `weekday` (precomputed so the client never parses dates), commits, additions, deletions. This is the year-long grid. |
| `weekly` | Per-week commits and lines changed, each bucket flagged for whether all seven days fall inside the range — so a clipped first week is not plotted as a cliff. |
| `cycle_time` | One point per merged PR: merge timestamp, hours open → merge, and the item it belongs to. Measured, never estimated. |
| `throughput` | Merged PRs and closed issues per week. |
| `contributors` | Per-contributor commits, additions, deletions, files changed and active days, with `is_agent` set for agent identities. |
| `work_kinds`, `work_states`, `labels` | Slice counts across work items — the composition of the backlog. |

The dashboard renders a subset (four stat cards, the cycle-time trend, the heatmap and the top
contributors); Insights renders the whole payload.

---

## Eng Health

`GET /api/health-metrics` returns `{ range, dora, bus_factor, review, quality }`.

### DORA, as far as git can honestly take it

| Metric | Derivation | Honesty note |
|---|---|---|
| **Cycle time p50 / p90** | Hours from PR open to merge, over merged PRs in range. `lead_time_samples` reports how many merged PRs produced a valid sample. | A true measurement, not an estimate. |
| **Change-failure rate** | Share of merged PRs whose title or labels read as a revert, hotfix or rollback. `null` when there were no merged PRs to judge. | A *text* proxy. It catches work that announces itself as a fix; it cannot see an incident that never produced a labelled PR. |
| **Merge frequency** | Merged PRs per week. | Straightforward count. |
| **Deploy proxy** | Merge commits per week (`Commit.is_merge`). | Named a proxy on purpose: gitstate has no CD or pipeline signal. It correlates with delivery cadence; it is not a deploy count and the UI never calls it one. |

Deployment frequency in the classic DORA sense would need CI/CD data gitstate does not have. Rather
than approximate it silently, the screen shows what git supports and labels the proxy.

The heavier SZZ analysis — blaming the lines a bug-fixing commit touched to find the commit that
introduced them — is not used here. It feeds the **quality** dimension of
[contribution](derivation.md), where the extra cost is worth it.

### Bus factor, review coverage, quality signals

- **Bus factor** — the fewest contributors whose combined commit share first reaches 50%, plus the top
  contributor's share and the full ordered list. Concentration risk, computed rather than guessed at.
- **Review coverage** — the share of merged PRs with at least one matching review, the count of
  merged-but-unreviewed PRs, and total reviews performed.
- **Quality signals** — test-touch rate (share of commits touching a test path), average commit size in
  lines, large-commit share (over 400 changed lines), and revert commits.

None of these are scores. They are counts and shares with their derivation stated, because the moment
a number like this becomes a target it stops measuring anything (see [Derivation model](derivation.md)).

---

## Involvement

`GET /api/involvement` answers "who touches what", in both directions:

- **By repo** — each repository's commit total and the contributors carrying it, with each person's
  share of that repo.
- **By person** — each contributor's footprint across every repo they touched in range.

It is derived from commit authorship, not from an org chart anybody maintains. Identities are the
merged ones from the [People](screenshots.md) view: the same human with three commit emails is one
row, and agent identities are marked rather than blended in.

---

## Contribution rollup and weights

`GET /api/contributions/rollup` accumulates each contributor's per-repo `Contribution` rows into one
six-dimension line across every repo, using the stored weights for the composite.

```
GET  /api/weights          → the six weights (default 1.0 each)
PUT  /api/weights          → validated + normalized to sum to 1, then persisted
POST /api/weights/reset    → back to all 1.0
```

The composite is a weighted mean:

```
composite = Σ(wᵢ · dᵢ) / Σwᵢ
```

The Contribution screen exposes this as a live tuner. That is not a gimmick — it is the argument: any
ordering of people is an artefact of the weights someone chose, so gitstate hands you the weights
instead of shipping a leaderboard and pretending its ranking is objective. `gitstate contributions
--weights shipped=2,review=1,…` does the same thing from the CLI.

Next: [Derivation model](derivation.md) · [Jira & Linear import](import.md) · [HTTP API](api.md)
