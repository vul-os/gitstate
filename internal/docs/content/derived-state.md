<!-- title: Derived State | order: 11 | category: Concepts | tier: Using gitstate | summary: How the git engine computes issue status, cycle time, and agent attribution. -->

# Derived State

How gitstate computes project truth from the git object graph. This is the engine behind
*derived, not entered*.

## The git engine

`internal/git` shells out to `git` (with sanitised input) and reads the repository directly:

| Function | What it reads |
|---|---|
| `Clone` / `Fetch` | maintain a local mirror in a per-repo cache dir |
| `WalkCommits(since)` | iterate commits with author, timestamp, file stats, and an `is_agent` flag |
| `Diff(sha)` | per-commit diff with numstat (additions/deletions per file) |
| `DiffRange(base, head)` | aggregate diff across a range — the unit fed to effort estimation |
| `Blame(path)` | line-level authorship |
| `LeadTime(firstCommitAt, mergedAt)` | the DORA lead-time duration |

Commits, PRs, and issues land in the `commits`, `pull_requests`, and `issues` tables. Everything is
org-scoped under RLS.

## Issue state, derived

You don't set issue status by hand for git-backed work. Status is a projection of linked PR state:

- **Open PR** referencing an issue → `in_progress`
- **Merged PR** referencing an issue → `done` (merged always wins)

See [Connecting repos → Auto-progress](/docs/connecting-repos) for the linking rules. Native
(non-repo) tasks keep manual status — that's the second truth-mode, see [Concepts](/docs/concepts).

## Cycle time (DORA)

When a PR is merged, gitstate writes a `cycle_times` row:

| Measure | Definition |
|---|---|
| `lead_time_secs` | first commit → merged (the full DORA lead time) |
| `review_secs` | PR created → merged (time in review / open state) |

Only merged PRs carry meaningful cycle-time data. These feed the cycle-time dashboards and calibrate
forecasts — see [Metrics & reporting](/docs/metrics-and-reporting).

## Agent-commit detection

In an agent-native world, gitstate flags agent-authored commits (`is_agent`) so attribution stays
honest. A commit is classified as agent-authored when any of these match (case-insensitive):

- Author **name** contains `[bot]`.
- Author **email** ends in `[bot]@users.noreply.github.com`.
- Author name/email or commit trailers match known agent patterns: `claude`, `copilot`, `cursor`,
  `devin`, `codeium`, `aider`, `amp`, `dependabot`, `gitstate-agent`, or a trailing `[bot]`.

The heuristic is **intentionally conservative**: a missed agent commit (false negative) is preferable
to mislabelling a human's work. The pattern list is extended as new agents emerge. Agent detection
*informs* — it never feeds a ranking or a bonus formula (see [Concepts → Agent-native](/docs/concepts)).

## Why this matters

Because state is derived, there are no stale tickets, no drag-to-done, and no Friday-afternoon
timesheet reconstruction. The repo is the ledger; gitstate just reads it. The parts git genuinely
cannot see are surfaced explicitly rather than guessed — see
[Effort & estimation](/docs/effort-and-estimation) and [Billing](/docs/billing).
