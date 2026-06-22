<!-- title: Quickstart | order: 3 | category: Getting Started | tier: Using gitstate | summary: Run the full stack locally and connect your first repo in minutes. -->

# Quickstart

Run the full stack locally in a few commands.

## Prerequisites

- **Go 1.25+**, **Node 20+**, and a **Postgres** database (local or [Neon](https://neon.com)).
- `git` on the `PATH` (the git engine shells out to it).

## Setup

```bash
git clone <repo> gitstate && cd gitstate

# 1. Configure — one shared env for backend + frontend
cp .env.example .env.dev          # point DATABASE_URL at your Postgres; set JWT_SIGNING_KEY etc.

# 2. Database (forward-only migrations)
go run ./cmd/migrate up
go run ./cmd/seed                 # demo org (login demo@gitstate.dev / demo1234)
go run ./cmd/seedgit              # real git-analysis → contribution durability/SZZ + DORA data
# (or just `make seed`, which runs both)

# 3. Run everything
cd web && npm install && npm run dev:full   # Go API on :8080 + Vite on :5173
```

Open **http://localhost:5173** and sign in.

> [!TIP]
> The `.env.dev` file is loaded automatically in dev. One file holds both backend secrets
> (unprefixed) and frontend-public vars (`VITE_*`); see [Configuration](/docs/configuration).

## First steps in the app

1. **Create an organisation** — the tenant root. Everything is scoped to it.
2. **Connect a repo** — point gitstate at a GitHub or GitLab repository and trigger a sync. See
   [Connecting repos](/docs/connecting-repos).
3. **Watch state derive** — issues auto-progress from linked PRs, cycle times compute, and (if an
   `ANTHROPIC_API_KEY` is set) effort estimates appear on the issue drawer.
4. **Ask a question** — use the natural-language report box on the dashboard, e.g. *"which PRs took
   longest to merge last month?"*. See [Metrics & reporting](/docs/metrics-and-reporting).

## Single binary

```bash
make build         # builds the web app, embeds it, builds the OSS binary
make build-ee      # Enterprise Edition (Paystack billing + cross-org admin)
./gitstate         # serves API + the embedded UI on :8080
```

See [Self-hosting](/docs/self-hosting) for Docker, fly.io, and systemd, and the
[CLI & tools](/docs/cli-and-tools) page for `migrate`, `seed`, and `billsim`.
