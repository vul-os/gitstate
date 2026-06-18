<!-- title: Self-hosting | order: 5 -->

# Self-hosting

gitstate ships as a single binary. Bring your own Postgres and you own the whole stack.

## Docker

```bash
docker compose up                 # app + (optional) local Postgres via the `local-db` profile
```

The `Dockerfile` is multi-stage: it builds the web app, embeds it into the Go binary, and produces a
minimal final image. Cloud images build with `-tags ee` to include billing and cross-org admin.

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

## Configuration

All config is environment variables (see `.env.example`) overlaid on `config.yaml`. Secrets live only
in the environment. OAuth providers appear on the login page only when their client ID + secret are set.

## Migrations

Forward-only, `YYYYMMDD_NNN_name.sql`. `go run ./cmd/migrate up` applies pending; `reset` is dev-only.
A rollback is a new migration.
