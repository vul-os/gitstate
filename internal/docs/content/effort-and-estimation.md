<!-- title: Effort & Estimation | order: 12 | category: Concepts | summary: An LLM reads the diff for semantic difficulty (1–10) — never line count. -->

# Effort & Estimation

Effort in gitstate is **evidence, not a guess.** An LLM reads the actual diff and judges *semantic
difficulty* — never line count, never a story-point field. This is the *evidence-based, gaps visible*
discipline applied to sizing.

## Diff-difficulty, not line count

When you estimate a PR, gitstate hands the LLM the aggregate diff (`DiffRange`) and asks for a
**difficulty score on a 1–10 scale based on semantic complexity**. The prompt is explicit:

> A 4-line concurrency fix can be harder than 400 lines of boilerplate.

The model judges on:

- Cognitive load required to understand the change
- Risk of subtle bugs or regressions
- Domain expertise required
- Breadth of impact across the codebase
- Algorithmic or architectural complexity

## What an estimate contains

Each estimate is returned as structured JSON and stored with the PR/issue for traceability:

| Field | Meaning |
|---|---|
| `difficulty` | 1.0–10.0 semantic difficulty |
| `rationale` | 2–3 sentence justification |
| `evidence.key_changes` | the major changes the model identified |
| `evidence.risk_factors` | risks / complexity drivers observed |
| `evidence.complexity_signals` | e.g. concurrency, state mutation, security boundary, perf-critical |
| `model` | which model produced the estimate (for auditability) |

Every estimate links back to the diff it was computed from. There is no anonymous number floating in
a ticket — the evidence travels with it.

## Estimate a PR

```http
POST /api/metrics/estimate/{prId}
Authorization: Bearer <token>
X-Org-ID: <org-id>
```

The estimate appears on the issue/PR drawer in the UI. If no LLM is configured, the call returns a
clear "not configured" error rather than fabricating a number — see [Configuration](/docs/configuration).

## Why not story points?

Story points are a human-invented number, off by ~30% on average, and gamed the moment they become a
target (Goodhart's Law). gitstate refuses to make a points field the source of truth. Instead:

- **Difficulty** comes from reading the real diff.
- **Forecasts** are calibrated from observed [cycle time](/docs/metrics-and-reporting), not points.

## Status synthesis (related)

The same LLM layer can synthesise a leadership-readable status summary from recent PRs, issues, and
commits — focused on *what shipped, what's at risk, open decisions*. The prompt explicitly forbids
individual rankings or performance judgments: it shows **work patterns, not worker scores**. This is
the same *texture, never a score* principle from [Concepts](/docs/concepts).
