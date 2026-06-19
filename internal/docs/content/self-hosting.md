<!-- title: Self-hosting | order: 31 | category: Operations | summary: Ship the single binary via Docker, fly.io, systemd, or bare metal. -->

# Self-hosting

gitstate ships as a single binary. Bring your own Postgres and you own the whole stack. The default
build is fully open (AGPL-3.0); only Paystack billing and cross-org admin live behind `-tags ee`.

## Build the binary

```bash
make build         # builds web, embeds it, produces ./gitstate (OSS)
make build-ee      # same, with -tags ee (Paystack billing + cross-org admin)
./gitstate         # serves the API and the embedded SPA on :8080
```

The `Dockerfile` is multi-stage: it builds the web app, embeds it into the Go binary, and produces a
minimal final image.

## Docker / Compose

```bash
docker compose up                 # app + (optional) local Postgres via the `local-db` profile
```

`docker-compose.yml` runs the app and, when you enable the `local-db` profile, a local Postgres for
pure self-host (no Neon required). Cloud images build with `-tags ee`.

## fly.io

```bash
fly launch --copy-config --no-deploy   # uses deploy/fly.toml (primary region jnb)
fly secrets set DATABASE_URL=... JWT_SIGNING_KEY=... TOKEN_ENC_KEY=...
fly deploy
```

## Plain binary / systemd

```bash
make build                         # produces ./gitstate
sudo cp gitstate /usr/local/bin/
sudo cp deploy/gitstate.service /etc/systemd/system/
sudo systemctl enable --now gitstate
```

## Required configuration

At minimum set `DATABASE_URL` and a strong `JWT_SIGNING_KEY` (32+ bytes). For at-rest repo-token
encryption set `TOKEN_ENC_KEY`. Everything else is optional and feature-gates itself:

| Feature | Enable by setting |
|---|---|
| Google / Microsoft login | the provider's client id **and** secret |
| LLM effort + NL reports | `ANTHROPIC_API_KEY` |
| Billing (EE build) | `billing.enabled: true` + Paystack + exchange keys |
| Super admin | `SUPER_ADMIN_EMAILS` |

Full reference: [Configuration](/docs/configuration). Secrets live only in the environment; OAuth
providers appear on the login page only when configured.

## Migrations

Forward-only, `YYYYMMDD_NNN_name.sql`. `go run ./cmd/migrate up` applies pending migrations; `reset`
is dev-only and refused when `GITSTATE_ENV=prod`. A rollback is a new forward migration. See
[CLI & tools](/docs/cli-and-tools).

## Operating notes

- The in-process rate limiter is per-instance. On multi-VM fly.io deployments, enforce limits with a
  shared backend (e.g. Redis) for global limits.
- The web SPA is embedded; a single binary serves both the API and the UI with SPA-fallback routing.
