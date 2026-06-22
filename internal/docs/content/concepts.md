<!-- title: Concepts | order: 10 | category: Concepts | tier: Using gitstate | summary: The mental model: derived state, two truth-modes, texture, free stakeholders. -->

# Concepts

The mental model behind gitstate. Read this once and the rest of the docs fall into place.

## Derived, not entered

The core idea: **state is computed from git, not typed by a human.** A merged PR means *done*; an
open PR linked to an issue means *in progress*. Nobody maintains tickets to keep them honest, because
there's nothing to maintain — the tickets are a projection of the repo.

This is why gitstate doesn't have a "story points" input field as a source of truth, and why you
never drag a card to "Done" — git did that for you when the PR merged.

## Two truth-modes, one board

Not all work lives in git. So gitstate runs **two truth-modes** on a single board:

| Mode | Source of truth | Examples | How state changes |
|---|---|---|---|
| **Git** (derived) | the repository | issues linked to PRs/commits, dev work | automatically, from PR/commit state |
| **Native** (manual) | the tool | design, research, marketing, ops tasks | edited by a human, like any tracker |

This is an honesty constraint, not a limitation: we only claim "derived truth" where it's actually
real. We never *infer* a designer's or marketer's contribution from git. Native tasks are first-class
records; git-backed issues are projections of git reality. Badges on the board tell you which mode an
item is in.

![the unified board](/shots/board.png)

## Involvement as texture (never a score)

Contribution is shown as **texture across multiple independent dimensions** — features shipped, review
load, areas owned, active/dormant — and **never collapsed into a single number, ranking, or bonus
formula.**

Why: the moment "contribution" is one number, it gets gamed (Goodhart's Law) and it erases the
invisible senior work — review, mentorship, ownership — that doesn't show up as authored commits.
gitstate answers *"who is relevant to this area?"* (routing), and deliberately refuses to answer
*"who is worth the most?"* (ranking). There is no `score` field anywhere in the data model.

See [Metrics & reporting](/docs/metrics-and-reporting) for the exact dimensions.

## Evidence-based, with visible gaps

Effort and billing are backed by git activity. The part git **can't** see — meetings, research,
pairing, planning — is **flagged for a human to confirm**, never silently invented. Invoices mark
those lines `is_estimated` with a `confirmation_required` flag. The principle: **under-count rather
than fabricate.** This is what makes an invoice defensible to a client.

## Free stakeholders

Billing is per **builder**. Clients, viewers, and other stakeholders are **free** — the
`org_members.role = stakeholder` role never counts toward seat billing. This is structural, not a
discount: per-seat incumbents can't match it without rewriting their revenue model. See
[Billing](/docs/billing).

## Agent-native

gitstate is built for a world where agents write code and humans supervise. Agent-authored commits are
detected and flagged (`is_agent`), so "who/what did this" stays honest as the mix shifts. Humans are
tracked as the **oversight layer** — to *inform*, never to formula-rank. See
[Derived state](/docs/derived-state) for how agent commits are detected.

## Roles at a glance

| Role | Counts as a seat? | Typical use |
|---|---|---|
| `owner` | yes (builder) | org founder / admin |
| `admin` | yes (builder) | team lead |
| `member` | yes (builder) | engineer |
| `stakeholder` | **no — free** | client, viewer, exec |
