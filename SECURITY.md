# Security Policy

## Reporting a vulnerability

Please report security issues privately to the maintainers rather than opening a public issue.
Include a description, reproduction steps, and impact. We aim to acknowledge within a few business days.

## Security model

gitstate's security model — multi-tenant isolation via Postgres **row-level security**, audited
super-admin access, JWT + rotating refresh tokens, env-only secrets, Paystack webhook signature
verification, rate limiting, and at-rest token encryption — is documented in
[`docs/security.md`](./docs/security.md), along with the residual hardening checklist.

Tenant isolation is enforced in the database (every org-scoped table has an RLS policy), and proven
by an automated test (`internal/store/rls_test.go`) that asserts cross-org reads return zero rows.
