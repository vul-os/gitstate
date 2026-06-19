<!-- title: Configuration | order: 30 | category: Operations | summary: Every config flag and environment variable, and how features gate themselves. -->

# Configuration

gitstate is configured by **`config.yaml` (structure + flags) overlaid with environment variables
(secrets) — env always wins.** One shared `.env` (or `.env.dev`) holds both backend secrets
(unprefixed) and frontend-public vars (`VITE_*`); Vite reads it via `envDir` at the repo root and
exposes **only** `VITE_*` to the browser, so secrets never reach the bundle.

Copy the templates:

```bash
cp .env.example .env.dev          # secrets + frontend-public vars
cp config.example.yaml config.yaml  # structure + flags
```

## Core

| Var | Purpose |
|---|---|
| `GITSTATE_ENV` | `dev` \| `prod` (migration `reset` is refused on `prod`) |
| `HTTP_ADDR` | listen address, e.g. `:8080` |
| `PUBLIC_URL` | public base URL (also the CORS origin) |
| `CONFIG_FILE` | path to `config.yaml` |

## Database

| Var | Purpose |
|---|---|
| `DATABASE_URL` | Neon/Postgres connection string |
| `DATABASE_MAX_CONNS` | pgx pool size |

`database.rls: true` keeps RLS enforcement on — every request runs inside a transaction with
`SET LOCAL app.current_org`.

## Auth / JWT

| Var | Purpose |
|---|---|
| `JWT_SIGNING_KEY` | HS256 access-token signing key (32+ bytes) |
| `ACCESS_TOKEN_TTL` | access token lifetime (default 15m) |
| `REFRESH_TOKEN_TTL` | refresh lifetime (default 720h / 30d) |

## OAuth — provider gating

OAuth providers are **config-gated**: a provider is enabled (and its button appears on the login page)
**only when both its client id and secret are set.** No dead buttons for self-hosters.

| Var | Provider |
|---|---|
| `OAUTH_GOOGLE_CLIENT_ID` / `OAUTH_GOOGLE_CLIENT_SECRET` | Google |
| `OAUTH_MICROSOFT_CLIENT_ID` / `OAUTH_MICROSOFT_CLIENT_SECRET` / `OAUTH_MICROSOFT_TENANT` | Microsoft |

The `/api/config` endpoint reports which providers are enabled so the frontend renders the right
buttons. `auth.providers.<p>.enabled` is **derived** (`id != "" && secret != ""`), never set by hand.

## Git platform sync

| Var | Purpose |
|---|---|
| `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET` | GitHub OAuth app |
| `GITLAB_OAUTH_CLIENT_ID` / `GITLAB_OAUTH_CLIENT_SECRET` | GitLab OAuth app |
| `GITHUB_APP_ID` / `GITHUB_APP_PRIVATE_KEY` | GitHub App (optional) |
| `TOKEN_ENC_KEY` | AES-256-GCM key for at-rest repo-token encryption |

## LLM (effort + reports)

| Var | Purpose |
|---|---|
| `LLM_PROVIDER` | provider, default `anthropic` |
| `ANTHROPIC_API_KEY` | provider key — **absent ⇒ LLM features no-op cleanly** (no fabricated numbers) |
| `LLM_MODEL` | model id, default `claude-sonnet-4-6` |

Without a key, effort estimation and NL→report return a clear "not configured" error and the
dashboard's prose synthesis is simply omitted.

## Billing (EE)

Only active in `-tags ee` builds **and** when `billing.enabled: true`.

| Var | Purpose |
|---|---|
| `PAYSTACK_SECRET_KEY` / `PAYSTACK_PUBLIC_KEY` / `PAYSTACK_WEBHOOK_SECRET` | Paystack |
| `BILLING_CURRENCY_BILL` / `BILLING_CURRENCY_CHARGE` | bill currency (USD) / charge currency (ZAR) |
| `EXCHANGE_PROVIDER` | `exchangerate-api` (default) \| `openexchangerates` |
| `EXCHANGE_API_KEY` / `EXCHANGE_TTL` | rate provider key / cache TTL (default 6h) |

The plan ladder lives under `billing.plans` in `config.yaml`. See [Billing](/docs/billing).

## Super admin

| Var | Purpose |
|---|---|
| `SUPER_ADMIN_EMAILS` | bootstrap allowlist for the admin console |

`admin.realtime: true` enables SSE-backed live dashboards.

## Frontend (VITE_*)

Safe to ship to the browser — **no secrets here.**

| Var | Purpose |
|---|---|
| `VITE_API_BASE_URL` | API base URL |
| `VITE_PUBLIC_URL` | public URL |
| `VITE_PAYSTACK_PUBLIC_KEY` | Paystack **public** key (mirror of `PAYSTACK_PUBLIC_KEY`) |
| `VITE_BILLING_CHARGE_CURRENCY` | charge currency label (ZAR) |

## Feature gating at a glance

| Feature | Turns on when… |
|---|---|
| Google / Microsoft login | provider id + secret set |
| Repo-token encryption | `TOKEN_ENC_KEY` set |
| LLM effort & NL reports | `ANTHROPIC_API_KEY` set |
| Billing | `-tags ee` build **and** `billing.enabled: true` |
| Super-admin console | email in `SUPER_ADMIN_EMAILS` or `users.is_super_admin` |
