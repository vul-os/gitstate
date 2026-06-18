# syntax=docker/dockerfile:1
# =============================================================================
# gitstate — multi-stage production image
#
# Stages
# ------
# 1. node-build   : npm ci + npm run build  →  web/dist/
# 2. go-build     : copies web/dist into internal/web/embed/, then
#                   go build -tags ee for the cloud binary (includes EE billing,
#                   Paystack, cross-org admin) plus cmd/migrate.
#                   Use `-tags ee` here because this Dockerfile is the cloud
#                   path (see decisions.md A7: default OSS build excludes ee).
#                   For an OSS-only image, remove `-tags ee` from the go build
#                   commands below.
# 3. final        : distroless/static-debian12 — no shell, minimal attack
#                   surface; just the two binaries + CA certs for TLS.
#
# Build args
# ----------
#   GO_VERSION   (default 1.25) — must match go.mod
#   NODE_VERSION (default 22)   — LTS; matches Vite 6 / React 19 requirements
# =============================================================================

ARG GO_VERSION=1.25
ARG NODE_VERSION=22

# -----------------------------------------------------------------------------
# Stage 1 — Node: build the React/Vite frontend
# -----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-slim AS node-build

WORKDIR /src/web

# Copy only manifests first for layer-cache efficiency.
COPY web/package.json web/package-lock.json ./

RUN npm ci --prefer-offline

# Copy the rest of the web source and build.
COPY web/ ./

RUN npm run build
# Output: /src/web/dist/

# -----------------------------------------------------------------------------
# Stage 2 — Go: build the server binaries
# -----------------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS go-build

WORKDIR /src

# Copy Go module files first for cache efficiency.
COPY go.mod go.sum ./
RUN go mod download

# Copy all Go source.
COPY . .

# Populate the embed directory with the compiled frontend so the binary carries
# the full SPA.  The .gitkeep is already present (committed); index.html +
# assets from the node stage replace it here.
COPY --from=node-build /src/web/dist/ ./internal/web/embed/

# Build the main server (cloud = EE build; see note at top of file).
RUN CGO_ENABLED=0 GOOS=linux \
    go build -tags ee -trimpath -ldflags="-s -w" \
    -o /out/gitstate ./cmd/gitstate

# Build the migration runner.
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/migrate ./cmd/migrate

# -----------------------------------------------------------------------------
# Stage 3 — Final: minimal distroless image
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS final

# Copy CA certificates (already in distroless/static) + compiled binaries.
COPY --from=go-build /out/gitstate /gitstate
COPY --from=go-build /out/migrate  /migrate

# gitstate listens on 8080 by default (matches fly.toml internal_port).
EXPOSE 8080

# Default entrypoint is the server; run migrate separately via
#   docker run --entrypoint /migrate <image> up
ENTRYPOINT ["/gitstate"]
