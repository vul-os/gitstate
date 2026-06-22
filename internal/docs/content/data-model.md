<!-- title: Data Model | order: 13 | category: Concepts | tier: Using gitstate | summary: The core tables gitstate derives state into, and the no-score invariant. -->

# Data Model

gitstate stores hand-written SQL over Postgres (no heavy ORM — see [Architecture](/docs/architecture)).
Every org-scoped table carries an `org_id` and a row-level-security policy, so isolation is enforced by
the database, not just the app. This page maps the tables the rest of the docs refer to.

## Identity & tenancy

| Table | Holds |
|---|---|
| `users` | accounts (argon2id password hash, optional `is_super_admin`) |
| `oauth_accounts` | linked Google / Microsoft identities |
| `refresh_tokens` | hashed, rotating refresh tokens (reuse detection) |
| `organizations` | the tenant root — everything is scoped under one |
| `org_members` | membership + `role` (`owner` · `admin` · `member` · free `stakeholder`) |
| `org_invites` | pending invite tokens |

The `role` column is load-bearing: `owner`/`admin`/`member` are **builders** (billable);
`stakeholder` is structurally **free**. See [Concepts → Free stakeholders](/docs/concepts).

## Git-derived state

These are the projection of the repository — the *derived, not entered* core.

| Table | Holds | Derived by |
|---|---|---|
| `repos` | connected GitHub/GitLab repos (+ optional `token_encrypted`) | [Connecting repos](/docs/connecting-repos) |
| `projects` | grouping for issues/boards | — |
| `issues` | unified GitHub/GitLab + native issues, with `derived_state` | auto-progress from linked PRs |
| `pull_requests` | PRs normalised to `open` / `merged` / `closed` | sync |
| `commits` | walked commits with author, stats, and `is_agent` | [git engine](/docs/derived-state) |
| `task_files` | files attached to native tasks | — |
| `platform_connections` | per-org OAuth-app connection tokens (GitHub/GitLab) | [Integrations](/docs/integrations) |

## Metrics — texture, never a score

| Table | Holds |
|---|---|
| `effort_estimates` | LLM diff-difficulty (1–10) + evidence + `model` |
| `cycle_times` | `lead_time_secs` (first commit → merged) and `review_secs` |
| `involvement` | independent dimensions — features shipped, reviews, areas owned, `active` |
| `agent_runs` | first-class agent execution records |

**Invariant:** there is no `score` column anywhere. Involvement is stored as independent dimensions
and **never** collapsed into a single number, ranking, or bonus formula. This is the
*texture, never a score* discipline made structural — see [Concepts](/docs/concepts) and
[Metrics & reporting](/docs/metrics-and-reporting).

## Capacity

| Table | Holds |
|---|---|
| `availability` | weekly hours + working days, dated by `effective_from` |
| `leave_entries` | PTO/sick/holiday with `pending` · `approved` · `rejected` status |
| `time_entries` | minutes against an issue, `source` = git-derived or manual |

Effective capacity = availability − **approved** leave. See [Capacity & planning](/docs/capacity-and-planning).

## Billing (EE)

| Table | Holds |
|---|---|
| `plans` | the plan ladder (incl. `per_builder_cents`, `included_llm_cents`, `overage_markup`) |
| `subscriptions` | an org's current plan |
| `usage_events` | metered usage (e.g. managed-LLM cost) |
| `exchange_rates` | cached USD↔ZAR rates with provider fallback |
| `invoices` / `invoice_lines` | evidence invoices; lines carry `is_estimated` + `confirmation_required` |
| `payments` | charge records |
| `paystack_events` | webhook idempotency keys (no double-charge) |
| `org_llm_settings` | per-org managed-vs-BYO LLM configuration |

See [Billing](/docs/billing).

## Platform (not org-scoped)

| Table | Holds |
|---|---|
| `audit_log` | every cross-org super-admin action (`actor`, `org`, `action`, `target`, `meta`) |
| `feature_flags` | platform feature toggles |

`audit_log` is deliberately **outside** RLS — it records super-admin access *across* orgs. See
[Security](/docs/security).
