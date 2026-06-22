<!-- title: Integrations | order: 21 | category: Using gitstate | tier: Using gitstate | summary: OAuth-app connect flow, PATs, and the LLM provider you bring. -->

# Integrations

gitstate integrates with the two places work actually happens: your **git platform** (GitHub /
GitLab) and an optional **LLM provider** (for effort estimation and natural-language reports).
Everything is opt-in and feature-gates itself when unconfigured.

## Git platforms

There are two ways to connect a repository.

### Personal access token

The simplest path: supply a read-scoped token directly when connecting a repo. This works for any
self-hoster with no OAuth app set up.

```http
POST /api/repos
{ "platform": "github", "fullName": "acme/widgets", "token": "<pat>" }
```

Tokens may be stored **encrypted at rest** (AES-256-GCM, keyed by `TOKEN_ENC_KEY`). See
[Connecting repos](/docs/connecting-repos) and [Security](/docs/security).

### OAuth-app connect flow

When a GitHub/GitLab OAuth app is configured (`GITHUB_OAUTH_CLIENT_ID` etc.), members can connect
their account once and pick from the repos they can access — no copy-pasting tokens.

```http
GET  /api/connect/{platform}/start    # begins the OAuth-app flow (self-authenticating)
GET  /api/connect/status              # [{platform, connected, login, configured}]
GET  /api/connect/{platform}/repos    # repos visible to the stored connection token
DELETE /api/connect/{platform}        # disconnect (deletes the stored encrypted token)
```

The connection token lives in `platform_connections`, org-scoped under RLS. The `/start` endpoint
self-authenticates from query params because a top-level browser navigation can't carry
`Authorization` / `X-Org-ID` headers.

| Platform | What it supports |
|---|---|
| GitHub | github.com (and GitHub App, optional via `GITHUB_APP_ID`) |
| GitLab | gitlab.com **and self-hosted** — pass a `baseURL` when connecting |

## LLM provider

The LLM powers [effort estimation](/docs/effort-and-estimation), the [NL→report](/docs/metrics-and-reporting)
path, and status synthesis. It is **bring-your-own** and entirely optional.

| Var | Purpose |
|---|---|
| `LLM_PROVIDER` | provider, default `anthropic` |
| `ANTHROPIC_API_KEY` | provider key — absent ⇒ LLM features no-op cleanly |
| `LLM_MODEL` | model id, default `claude-sonnet-4-6` |

Without a key, those features return a clear *"not configured"* error rather than fabricating a
number — the *evidence, not a guess* discipline. EE deployments can also offer a **managed** LLM with
a per-builder allowance (`org_llm_settings`, `included_llm_cents`); see [Billing](/docs/billing) and
[Configuration](/docs/configuration).

## Billing provider (EE)

Charging runs through **Paystack** in EE builds, with HMAC-SHA512-verified, idempotent webhooks. The
OSS build links a no-op stub. See [Billing](/docs/billing).
