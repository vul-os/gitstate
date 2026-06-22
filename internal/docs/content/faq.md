<!-- title: FAQ | order: 28 | category: Help | tier: Using gitstate | summary: Short answers to the questions people ask first. -->

# FAQ

## Do I have to maintain tickets?

No — that's the whole point. For git-backed work, state is **derived**: a merged PR is `done`, an open
PR is `in_progress`. Non-dev work uses native tasks (the second truth-mode) that you edit like any
tracker. See [Concepts](/docs/concepts).

## How is effort estimated? Is it just lines of code?

No. An LLM reads the actual diff and judges **semantic difficulty** on a 1–10 scale — a 4-line
concurrency fix can score higher than 400 lines of boilerplate. Every estimate carries evidence and
the model id. See [Effort & estimation](/docs/effort-and-estimation).

## Is involvement a productivity score?

No, and deliberately so. Involvement is **texture** across independent dimensions (features shipped,
reviews done, areas owned, active). There is no composite score, ranking, or bonus formula — and no
`score` column in the data model. It answers *"who is relevant?"*, never *"who is worth the most?"*
See [Metrics & reporting](/docs/metrics-and-reporting).

## Can the natural-language report tool modify my data?

No. The NL→report path is read-only by four independent layers: a constrained prompt, a `validateSQL`
denylist (SELECT-only, no semicolons, no mutation/DDL keywords), a `READ ONLY` transaction with a 5s
timeout, and RLS. Identity/billing/audit tables aren't even in the queryable allowlist. See
[Security](/docs/security).

## How does multi-tenant isolation work?

PostgreSQL Row-Level Security. Every org-scoped table filters on `app.current_org`, set inside each
request transaction. An app bug can't leak across orgs because the database enforces it — proven by a
cross-org test. See [Security](/docs/security).

## Why bill in USD but charge in ZAR?

USD is the pricing anchor; ZAR is charged via Paystack using the exchange rate captured **at charge
time**, stamped onto the invoice for auditability. This protects margin from FX drift. See
[Billing](/docs/billing).

## Are clients and viewers charged?

No. Billing is per **builder** (owner/admin/member). **Stakeholders are free** — structurally, not as
a discount. See [Concepts → Free stakeholders](/docs/concepts).

## What happens if I don't configure an LLM?

LLM features (effort estimation, NL→report, status synthesis) no-op cleanly with a clear "not
configured" message — gitstate never fabricates a number to fill the gap. Set `ANTHROPIC_API_KEY` to
enable them. See [Configuration](/docs/configuration).

## Does it work with GitLab, including self-hosted?

Yes. GitHub and GitLab are both supported, and self-hosted GitLab works by passing a `baseURL` when
connecting the repo. See [Connecting repos](/docs/connecting-repos).

## How are agent-written commits handled?

They're detected (`is_agent`) via a conservative heuristic on author name/email and known agent
trailers (claude, copilot, cursor, devin, dependabot, …). gitstate is built agent-native: agents
write code, humans supervise. Detection informs — it never feeds a ranking. See
[Derived state](/docs/derived-state).

## Is gitstate open source?

The core is **AGPL-3.0** and fully self-hostable. The `ee/` directory (Paystack billing, cross-org
admin) is commercial and compiled only with `-tags ee`. See [Architecture](/docs/architecture) and
[Self-hosting](/docs/self-hosting).

## How do I roll back a migration?

You don't roll back — you write a new forward migration. `reset` (drop + reapply) exists for dev only
and is refused on prod. See [CLI & tools](/docs/cli-and-tools).
