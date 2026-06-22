<!-- title: Configuration | order: 41 | category: Self-hosting & operations | tier: Developers & contributors | summary: Every config flag and environment variable, and how features gate themselves. -->

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

## Login — "Sign in with GitHub/GitLab"

Login is **email/password plus "Sign in with GitHub/GitLab"** (developer identities). Each "Sign in
with X" button appears **only when that platform's client id and secret are set** — no dead buttons
for self-hosters. The `/api/config` endpoint reports which login providers (`github`/`gitlab`) are
enabled so the frontend renders the right buttons. `auth.providers.<p>.enabled` is **derived**
(`id != "" && secret != ""`), never set by hand.

GitHub/GitLab use **one OAuth app per platform with incremental scopes**: login requests identity
scopes only (`read:user` + `user:email` for GitHub, `read_user` for GitLab); the separate
[Connect repositories](/docs/connecting-repos) step re-requests the heavier repo scopes. The same
`GITHUB_OAUTH_CLIENT_ID/SECRET` and `GITLAB_OAUTH_CLIENT_ID/SECRET` (below) power **both** login and
connect — so signing in with GitHub does **not** auto-grant repo access; you still click Connect.

On login the account email comes from the OAuth profile (GitHub's verified primary via
`/user/emails`). If the user hid their email we store a `@users.noreply.github.com` /
`@users.noreply.gitlab.com` placeholder and **Settings → Account** prompts for a real contact email
(`GET` / `PATCH /api/profile`).

## Git platform sync (login + connect)

These credentials power both "Sign in with GitHub/GitLab" and the repo-connect flow:

| Var | Purpose |
|---|---|
| `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET` | GitHub OAuth app (login + connect) |
| `GITLAB_OAUTH_CLIENT_ID` / `GITLAB_OAUTH_CLIENT_SECRET` | GitLab OAuth app (login + connect) |
| `GITHUB_APP_ID` / `GITHUB_APP_PRIVATE_KEY` | GitHub App (optional) |
| `TOKEN_ENC_KEY` | AES-256-GCM key for at-rest repo-token encryption |

**Callback URL setup:** register `$PUBLIC_URL/` as the OAuth app's authorization callback so that
**both** `/auth/oauth/<p>/callback` (login) and `/api/connect/<p>/callback` (connect) match the
registered base.

## Calendar (Google / Microsoft)

Google and Microsoft OAuth are used **only for the Calendar integration** (leave/availability sync in
Settings) — they are **not** login buttons. Each is config-gated by its own client id + secret.

| Var | Provider |
|---|---|
| `OAUTH_GOOGLE_CLIENT_ID` / `OAUTH_GOOGLE_CLIENT_SECRET` | Google Calendar |
| `OAUTH_MICROSOFT_CLIENT_ID` / `OAUTH_MICROSOFT_CLIENT_SECRET` / `OAUTH_MICROSOFT_TENANT` | Microsoft Calendar |

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
| "Sign in with GitHub/GitLab" | that platform's git oauth id + secret set |
| Calendar (Google / Microsoft) | `OAUTH_GOOGLE_*` / `OAUTH_MICROSOFT_*` id + secret set |
| Repo-token encryption | `TOKEN_ENC_KEY` set |
| LLM effort & NL reports | `ANTHROPIC_API_KEY` set |
| Billing | `-tags ee` build **and** `billing.enabled: true` |
| Super-admin console | email in `SUPER_ADMIN_EMAILS` or `users.is_super_admin` |
