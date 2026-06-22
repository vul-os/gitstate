<!-- title: Roadmap & Status | order: 60 | category: Project | tier: Developers & contributors | summary: What's shipped, what's Enterprise-only, and where gitstate is heading. -->

# Roadmap & Status

A candid map of what works today, what lives behind the Enterprise build, and the direction of travel.
gitstate's rule applies to its own roadmap too: claim only what's real.

## Shipped & open (AGPL-3.0)

These ship in the default OSS binary and are fully self-hostable.

| Area | Status |
|---|---|
| Git engine — clone/fetch, walk commits, diff, blame, lead-time | ✅ |
| Agent-commit detection (`is_agent`, conservative heuristic) | ✅ |
| GitHub + GitLab sync (incl. self-hosted GitLab), auto-progress | ✅ |
| Unified board — git-derived + native truth-modes | ✅ |
| Cycle time (DORA) + involvement texture | ✅ |
| Dashboards, burndown, and the SELECT-only NL→report path | ✅ |
| Capacity — availability, leave, time tracking | ✅ |
| RLS multi-tenancy + super-admin audit log | ✅ |
| At-rest repo-token encryption (AES-256-GCM) | ✅ |
| OAuth-app connect flow (GitHub/GitLab) + Google/Microsoft login | ✅ |

## Optional — bring-your-own

These light up when you provide a key, and no-op cleanly otherwise.

| Feature | Enable by setting |
|---|---|
| LLM effort estimation, NL→report, status synthesis | `ANTHROPIC_API_KEY` |
| Google / Microsoft login | the provider's client id **and** secret |
| Repo-token encryption | `TOKEN_ENC_KEY` |

See [Configuration](/docs/configuration) for the full gating table.

## Enterprise (EE — `-tags ee`)

Compiled only with `-tags ee` and runtime-gated by `billing.enabled`. The OSS build links no-op stubs,
so self-hosters can run gitstate fully without any of this.

| Feature | Notes |
|---|---|
| Paystack billing + evidence invoices | per-builder pricing, USD-billed / ZAR-charged |
| Managed LLM with per-builder allowance | `org_llm_settings`, included allowance + overage markup |
| Cross-org super-admin console | every cross-org action writes an `audit_log` entry |

See [Billing](/docs/billing) and [Architecture → Open core](/docs/architecture).

## Known limitations

Honesty includes the rough edges:

- **Rate limiting is per-instance.** The in-process token-bucket limiter isn't shared across VMs; on
  multi-VM fly.io deployments, front it with a shared backend (e.g. Redis) for global limits. See
  [Self-hosting](/docs/self-hosting).
- **Effort & NL features need a key.** Without `ANTHROPIC_API_KEY` they return a clear "not
  configured" error — by design, gitstate never fabricates a number to fill the gap.
- **Sync is on-demand.** Repos are synced when you trigger a sync; there is no opinionated polling
  scheduler in the OSS core.

## Direction

gitstate is built for the arriving world where **agents write the code and humans supervise**.
Agent runs are already first-class (`agent_runs`, `is_agent`); the trajectory is to make agent-native
project management — routing, oversight, and evidence — the default, without ever collapsing people
into a score. See [The Wedge](/docs/the-wedge).
