# Makefile — gitstate development & build tasks
#
# Prerequisites: Go 1.25+, Node 22+ (npm), Docker + Compose.
#
# Quick start (dev):
#   make dev          — start the Go server (uses .env / .env.dev config)
#   cd web && npm run dev   — start Vite dev server on :5173
#
# Build & ship:
#   make web          — compile frontend → internal/web/embed/
#   make build        — OSS binary (no ee tag)
#   make build-ee     — cloud binary (with -tags ee)
#   make docker       — build the Docker image (multi-stage, EE)

.PHONY: dev web build build-ee migrate seed docker

# ─── Config ──────────────────────────────────────────────────────────────────

BINARY   := gitstate
MIGRATE  := migrate
CMD_SRV  := ./cmd/gitstate
CMD_MIG  := ./cmd/migrate
WEB_SRC  := web
WEB_DIST := $(WEB_SRC)/dist
EMBED    := internal/web/embed
LDFLAGS  := -trimpath -ldflags="-s -w"

# ─── dev ─────────────────────────────────────────────────────────────────────

## dev: run the Go server in development mode (reloads config from .env).
## The Vite dev server (cd web && npm run dev) handles the frontend separately.
dev:
	go run $(CMD_SRV)

# ─── web ─────────────────────────────────────────────────────────────────────

## web: build the React/Vite frontend and copy the output into the Go embed dir.
## Run this before `make build` / `make build-ee` to get the SPA bundled into
## the binary.
web:
	cd $(WEB_SRC) && npm ci --prefer-offline && npm run build
	@echo "→ copying dist to embed dir"
	@mkdir -p $(EMBED)
	@cp -r $(WEB_DIST)/. $(EMBED)/
	@echo "✓ web embed ready"

# ─── build ───────────────────────────────────────────────────────────────────

## build: compile the OSS binary (no EE features).
## Run `make web` first if you want the SPA embedded.
build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) $(CMD_SRV)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(MIGRATE) $(CMD_MIG)
	@echo "✓ $(BINARY) + $(MIGRATE) built (OSS)"

## build-ee: compile the EE/cloud binary (Paystack billing + cross-org admin).
## Use this for the fly.io / Docker cloud deployment.
build-ee:
	CGO_ENABLED=0 go build -tags ee $(LDFLAGS) -o $(BINARY) $(CMD_SRV)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(MIGRATE) $(CMD_MIG)
	@echo "✓ $(BINARY) + $(MIGRATE) built (EE)"

# ─── migrate ─────────────────────────────────────────────────────────────────

## migrate: run pending migrations against DATABASE_URL (reads .env automatically).
migrate:
	go run $(CMD_MIG) up

## seed: run the demo seed (synthetic org + git history for local dev / demo).
seed:
	go run $(CMD_MIG) seed

# ─── docker ──────────────────────────────────────────────────────────────────

## docker: build the production Docker image (multi-stage, EE, distroless final).
docker:
	docker build -t gitstate:latest .
	@echo "✓ gitstate:latest built"
