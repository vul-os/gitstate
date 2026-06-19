<!-- title: Overview | order: 1 | category: Getting Started | summary: What gitstate is, the five disciplines, and what's in the box. -->

# Overview

**gitstate is the project tracker nobody updates by hand.** It reads your repositories and
*derives* true project state, effort, and — for billing teams — the invoice, directly from git.

![gitstate dashboard](/shots/dashboard.png)

Every other tracker (Jira, Linear, ClickUp, ZenHub) is a manually-maintained fiction sitting next
to git: people re-type into tickets what they already did in the repo. Estimates are made up,
velocity gets gamed, and timesheets are reconstructed Friday from memory. **Git is the real ledger.**
gitstate stops asking humans to invent numbers and observes them from git instead — making whatever
fiction remains *explicit*.

## The five disciplines

Every feature traces back to one of these. If a feature would force a human to invent a number, it
doesn't ship.

- **Derived, not entered** — state comes from git (merged = done, PR open = in progress). Nobody maintains tickets.
- **Measure work, not workers** — contribution is *texture* across dimensions (including review), never a single score.
- **Evidence-based, gaps visible** — effort and billing are backed by git; what git can't see is flagged, never invented.
- **Free stakeholders** — billing is per *builder*; clients and viewers are free.
- **Agent-native** — agent runs are first-class; humans are the oversight layer.

## What's in the box

| Area | What it does | Read more |
|---|---|---|
| **Git engine** | Clones repos, walks commits, reads diffs/blame/lead-time, detects agent commits | [Derived state](/docs/derived-state) |
| **Sync** | Two-way GitHub + GitLab issue/PR sync into one board; auto-progress from git | [Connecting repos](/docs/connecting-repos) |
| **Effort** | LLM reads the diff and judges *semantic difficulty* (1–10), with evidence | [Effort & estimation](/docs/effort-and-estimation) |
| **Metrics** | Cycle time (DORA) and involvement *texture* per person/project | [Metrics & reporting](/docs/metrics-and-reporting) |
| **Reporting** | Dashboards, burndown, and natural-language → report over a SELECT-only, RLS-safe path | [Metrics & reporting](/docs/metrics-and-reporting) |
| **Capacity** | Availability, PTO/leave, time tracking; capacity = availability − approved leave | [Capacity & planning](/docs/capacity-and-planning) |
| **Billing (EE)** | Per-builder plans, free stakeholders, USD-billed / ZAR-charged, evidence invoices | [Billing](/docs/billing) |

## Where to go next

- New here? Start with the [Quickstart](/docs/quickstart).
- Want the *why*? Read [The Wedge](/docs/the-wedge) and the [Concepts](/docs/concepts) mental model.
- Self-hosting? See [Self-hosting](/docs/self-hosting) and [Configuration](/docs/configuration).
- Building against the API? See the [API Reference](/docs/api-reference) and the [Data model](/docs/data-model).
- Curious what's shipped vs. Enterprise-only? See [Roadmap & status](/docs/roadmap-and-status).
