# Derivation model

gitstate never asks a human to invent a number. Project state and contribution are **derived** from
git and your forge, with the parts git can't see left explicitly visible rather than fabricated.

---

## Project state

`ProjectState` is computed per repo from the history walk plus a forge snapshot (PRs, issues, reviews):

| Field | Derived from |
|---|---|
| `open_prs` / `merged_prs` / `draft_prs` | forge PR states |
| `open_issues` / `closed_issues` | forge issue states |
| `in_progress` | open PR ⇒ in progress |
| `done` | merged PR / closed issue ⇒ done |
| `cycle_time_p50_hours` / `p90` | first-commit → merge, the DORA lead time — **not** a story-point input |
| `change_failure_rate` | reverts + SZZ-linked fixes over shipped changes |

Anything git and the forge cannot observe (meetings, research, product decisions) is surfaced in
`warnings`, never silently rolled into a number.

---

## Six dimensions of contribution

Contribution is shown as **texture across six dimensions**, normalized within the repo cohort — never a
single leaderboard score, never a bonus formula.

| Dimension | Evidence |
|---|---|
| **Shipped** | Merged PRs and closed issues attributable to the contributor. |
| **Review** | Reviews delivered to *others'* work — so reviewers and mentors aren't zeroed. |
| **Effort** | Σ of judged diff-difficulty (see [Classification & effort](classification.md)). |
| **Quality** | Inverts reverts caused and SZZ bug introductions — more breakage ⇒ lower. |
| **Ownership** | Number of distinct areas of the tree the contributor holds. |
| **Durability** | Surviving ÷ authored lines, from `git blame` — does the code last? |

Each dimension carries its **raw evidence** (`DimensionRaw`) alongside the 0–100 normalized value, so a
score is always traceable to counts. A `composite` is available as a *texture* summary but is display
only — gitstate deliberately does not rank people.

### Normalization

`normalize_dim(raw, cohort)` maps a raw value to 0–100 relative to the repo's cohort of contributors.
A single-member cohort yields `50.0` (no basis for spread). This keeps dimensions comparable *within a
repo* without implying cross-repo or cross-team ranking.

---

## git-only inputs

The `gitstate-git` crate derives everything from the local repository:

- **History walk** (`walk_commits`) — additions, deletions, files changed, merge flag, and a
  test-touch flag (did the commit touch a test path?). Only the first summary line is stored; **never
  source code**.
- **Blame survival** (`blame_survival`) — surviving vs. authored lines per author, feeding durability.
  Capped (4,000 files / 2 MiB) so large repos stay responsive.
- **SZZ** (`szz_bug_intros`) — links fix commits back to the changes that introduced the bug, feeding
  quality and change-failure. Capped (1,500 fixes / 40 files).
- **Diff summaries** (`diff_summary`) — shape only (adds/dels/files/languages/paths + title/body), the
  input to effort judging. No raw source leaves the machine.

---

## Merged identities

People commit under several emails and forge handles. `merge_contributor_identities` clusters aliases
into one `Contributor` (with `is_agent` / `agent_kind` for bots and coding agents). Human and agent
commits are counted **separately** and shown as a share (`agent_pct`) — gitstate is agent-native, so
autonomous runs are first-class units and humans are the oversight layer, but the two are never blended
into a single figure.

Next: [Classification & effort](classification.md) · [Contexts & P2P sync](contexts-sync.md)
