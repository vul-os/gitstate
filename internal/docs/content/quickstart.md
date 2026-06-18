<!-- title: Quickstart | order: 2 -->

# Quickstart

Run the full stack locally in a few commands.

## Prerequisites

- Go 1.25+, Node 20+, and a Postgres database (local or [Neon](https://neon.com)).

## Setup

```bash
git clone <repo> gitstate && cd gitstate

# 1. Configure — one shared env for backend + frontend
cp .env.example .env.dev          # point DATABASE_URL at your Postgres; set JWT_SIGNING_KEY etc.

# 2. Database (forward-only migrations)
go run ./cmd/migrate up
go run ./cmd/seed                 # optional: demo org (login demo@gitstate.dev / demo1234)

# 3. Run everything
cd web && npm install && npm run dev:full   # Go API on :8080 + Vite on :5173
```

Open **http://localhost:5173** and sign in.

## Single binary

```bash
make build         # builds the web app, embeds it, builds the OSS binary
make build-ee      # Enterprise Edition (Paystack billing + cross-org admin)
./gitstate         # serves API + the embedded UI on :8080
```

See [Self-hosting](/docs/self-hosting) for Docker and fly.io.
