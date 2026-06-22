<!-- title: Security | order: 42 | category: Self-hosting & operations | tier: Developers & contributors | summary: RLS tenancy, audited admin, webhook verification, and at-rest encryption. -->

# Security

This page mirrors the project's security model. It describes the properties gitstate enforces and the
residual risks to address before a production deployment.

## RLS is the tenancy boundary (S1)

Every org-scoped table (`repos`, `projects`, `issues`, `pull_requests`, `commits`, …) has PostgreSQL
Row-Level Security with the policy:

```sql
CREATE POLICY org_isolation ON <table>
  USING      (org_id = current_org())
  WITH CHECK (org_id = current_org());
```

`current_org()` reads `current_setting('app.current_org', true)::uuid`. The setting is injected by
`db.WithOrg(ctx, orgID, fn)`, which opens a transaction and runs `SET LOCAL app.current_org = $1`
before `fn`. `SET LOCAL` is transaction-scoped, so it can't bleed across requests.

**Invariant:** no org-scoped query runs outside a `WithOrg` block. An application bug *cannot* produce
a cross-org read, because the database enforces isolation independently. This is proven by
`internal/store/rls_test.go::TestRLSCrossOrgIsolation` — two orgs, one project each, asserting a read
under org A returns zero of org B's rows. The [NL→report path](/docs/metrics-and-reporting) relies on
this: org id is never interpolated into generated SQL; RLS does the isolation.

## Super-admin is audited, never ambient (S2)

Cross-org access is available only to super-admins via the EE admin interface (`ee/admin`). Every
cross-org operation calls `store.WriteAudit` before doing work, writing to the platform `audit_log`
table (not org-scoped, not under RLS): `actor_id`, `org_id`, `action`, `target`, `meta` (JSONB),
`created_at`. There is no silent god-mode session — each org touched generates an explicit audit entry.

## Secret hygiene — env only (S3)

- All secrets (`JWT_SIGNING_KEY`, Paystack keys, OAuth secrets, `TOKEN_ENC_KEY`, `ANTHROPIC_API_KEY`)
  live in environment variables.
- `.env` / `.env.dev` are gitignored and never committed; only `.env.example` is.
- `config.yaml` (committed) holds non-secret structure and flags only — no credentials.

See [Configuration](/docs/configuration) for the full variable reference.

## Webhook verification (S4)

Paystack webhooks (`ee/billing`) are verified with **HMAC-SHA512** over the raw request body,
compared **constant-time** against the `X-Paystack-Signature` header. Failing requests are rejected
with `401` before any processing. Processed event ids are stored in `paystack_events`, so duplicate
deliveries are detected and no-opped — preventing double-charges.

## Rate limiting

`middleware.RateLimit(perMin)` is a per-IP token-bucket limiter (in-memory, mutex-guarded, idle-bucket
cleanup). The global chain applies `RateLimit(300)`; auth endpoints can use the stricter
`AuthRateLimit()` to slow credential brute-forcing.

> [!WARNING]
> The limiter is in-process. On multi-VM fly.io deployments, replace it with a shared
> (e.g. Redis) backend so limits are global, not per-instance.

## At-rest token encryption

Repo access tokens (GitHub/GitLab PATs) may be stored in `repos.token_encrypted` (added by migration
`20260618_003_repo_tokens.sql`) using **AES-256-GCM** (`internal/crypto`):

- Key derived from `TOKEN_ENC_KEY` via SHA-256 → 32-byte AES key.
- `Encrypt` → `nonce(12) || ciphertext+GCM tag`; `Decrypt` is authenticated (tampered bytes error).
- Pure stdlib (`crypto/aes`, `crypto/cipher`, `crypto/rand`, `crypto/sha256`).
- `store.SetRepoToken` / `store.GetRepoToken` persist/retrieve the encrypted bytes inside an
  org-scoped (RLS) transaction; key handling is the caller's responsibility.

## Auth tokens

Access tokens are short-lived JWTs (HS256, default 15m). Refresh tokens (default 30d) are stored
hashed and **rotated on use**; reuse detection revokes the token family. Passwords are hashed with
**argon2id**.
