<!-- title: The Wedge | order: 2 | category: Getting Started | summary: Why gitstate exists and why the incumbents structurally can't copy it. -->

# The Wedge

Why does gitstate exist, and why can't the incumbents just copy it?

![tracker vs git, compared](/shots/compare.png)

## The problem is structural

Project trackers are a parallel, hand-maintained record of work that *already happened in git*.
That record is unreliable by construction:

- **Estimates** are off by ~30% on average — and have been for 40 years.
- **Velocity** becomes a vanity metric the moment it's a target (Goodhart's Law); teams inflate points.
- **Billable hours** are reconstructed from memory, leaking 15–25% of revenue.

These aren't bugs in the tools. They're what happens when you ask a human to *invent* a number.

## The move

Stop asking. **Observe from git** — the one honest ledger of what was actually done.

- Merged means done. A PR's first-commit-to-merge *is* the cycle time (this is DORA).
- An LLM reading the diff judges *semantic difficulty* — not line count.
- For agencies, the commits *become* the evidence-backed invoice; work git can't see (meetings, research)
  is flagged for a human to confirm, never fabricated.

## Why it's un-copyable

Incumbents are structurally blocked: their entire data model is hand-entered tickets, and their
revenue is per-seat. gitstate reads the git object graph directly, charges per *builder* (stakeholders
free), and is built for the arriving world where agents write the code and humans supervise.

## Beachhead and expansion

| Stage | Who | Why now |
|---|---|---|
| **Beachhead** | Client-billing dev shops (agencies, consultancies) | Acute pain — they bleed on manual time-tracking and defending invoices today |
| **Expansion** | Scaling multi-repo teams | Cross-repo derived state without the ticket-maintenance tax |
| **End game** | Agent-native PM | Agents write the code; humans supervise — incumbents' ticket model doesn't survive this |

The next step is the [Concepts](/docs/concepts) mental model, which turns these principles into the
exact mechanics gitstate uses.
