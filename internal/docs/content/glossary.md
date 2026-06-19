<!-- title: Glossary | order: 43 | category: Reference | summary: Plain-English definitions for the terms used across these docs. -->

# Glossary

The vocabulary gitstate uses, in one place. Most terms have a fuller treatment elsewhere — follow the
links.

## Agent commit
A commit authored by an AI agent, detected via a conservative heuristic on author name/email and known
trailers (`claude`, `copilot`, `cursor`, `devin`, `dependabot`, …) and flagged `is_agent`. Detection
**informs** attribution; it never feeds a ranking. See [Derived state](/docs/derived-state).

## Builder
A member who counts toward seat billing — role `owner`, `admin`, or `member`. Contrast with
**stakeholder**. See [Billing](/docs/billing).

## Cycle time (DORA)
The time a change takes to ship. gitstate records `lead_time_secs` (first commit → merged) and
`review_secs` (PR opened → merged) for every merged PR. See [Metrics & reporting](/docs/metrics-and-reporting).

## Derived state
Project state **computed from git** rather than typed by a human: merged PR ⇒ `done`, open PR ⇒
`in_progress`. The founding discipline. See [Derived state](/docs/derived-state).

## Effective capacity
`availability − approved leave`. Only **approved** leave subtracts. See
[Capacity & planning](/docs/capacity-and-planning).

## Effort / difficulty
A 1–10 **semantic difficulty** score an LLM assigns by reading the actual diff — not line count, not
story points. Carries evidence and the model id. See [Effort & estimation](/docs/effort-and-estimation).

## Evidence invoice
An invoice whose lines are backed by git activity; work git can't prove is flagged `is_estimated` with
`confirmation_required` rather than silently charged. *Under-count rather than fabricate.* See
[Billing](/docs/billing).

## Involvement (texture)
Contribution shown as independent, observable dimensions (features shipped, reviews done, areas owned,
active) — **never** collapsed into a score, ranking, or bonus. Answers *"who is relevant?"*, never
*"who is worth the most?"*. See [Metrics & reporting](/docs/metrics-and-reporting).

## NL→report
Ask the database a question in plain English; gitstate generates a `SELECT`, runs it under four
independent safety layers, and returns **both the rows and the SQL**. See
[Metrics & reporting](/docs/metrics-and-reporting).

## Native task
A manually-edited work item for non-git work (design, research, ops) — the second of the **two
truth-modes**. Contrast with a git-backed issue, which is a projection of the repo. See
[Concepts](/docs/concepts).

## Org (organisation)
The tenant root. Everything is scoped under one org and isolated by RLS. See
[Data model](/docs/data-model).

## RLS (Row-Level Security)
The Postgres mechanism that enforces multi-tenant isolation: every org-scoped table filters on
`app.current_org`, set inside each request transaction by `db.WithOrg`. The tenancy boundary. See
[Security](/docs/security).

## Stakeholder
A free, non-billable role (`org_members.role = stakeholder`) for clients, viewers, and execs.
Structural, not a discount. See [Concepts → Free stakeholders](/docs/concepts).

## Two truth-modes
The single board runs two modes: **git** (state derived from the repo) and **native** (state edited by
a human). gitstate only claims "derived truth" where it's actually real. See [Concepts](/docs/concepts).
