# gitstate node image. Build from the repository root:
#
#   docker build -f deploy/Dockerfile -t gitstate .
#
# See docs/DEPLOYMENT.md. The image serves the JSON API and, if you build the SPA
# into it, the web app; it does not terminate TLS — put a proxy in front.

# ─────────────────────────── build ───────────────────────────
FROM rust:1.85-bookworm AS build

# rusqlite is vendored+bundled, so it compiles SQLite itself and needs no libsqlite3.
# git2 is built with default-features off (no libssh2, no system libgit2), so the
# only system dependency left is a C toolchain, which the rust image already has.
WORKDIR /src

# Copy the manifests first so a dependency layer can be cached independently of
# the sources. `--locked` is not optional here: an image that silently resolves a
# different dependency graph than the committed lockfile is not the artifact that
# was tested.
COPY Cargo.toml Cargo.lock ./
COPY crates ./crates

RUN cargo build --release --locked -p gitstate-daemon -p gitstate-cli \
 && strip target/release/gitstated target/release/gitstate

# ─────────────────────────── runtime ───────────────────────────
FROM debian:bookworm-slim

# ca-certificates: the forge clients and the peer transport speak HTTPS.
# curl: the HEALTHCHECK below actually asks the running daemon whether it is
# serving. A healthcheck that only proves the binary can print its own --help
# would report "healthy" for a process that never bound its socket, which is worse
# than having no healthcheck at all.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

# A fixed non-root uid, so a bind-mounted host directory has predictable ownership.
RUN useradd --system --uid 10001 --home /var/lib/gitstate --shell /usr/sbin/nologin gitstate \
 && install -d -o gitstate -g gitstate -m 0700 /var/lib/gitstate

COPY --from=build /src/target/release/gitstated /usr/local/bin/gitstated
COPY --from=build /src/target/release/gitstate  /usr/local/bin/gitstate

USER gitstate
WORKDIR /var/lib/gitstate

# A container's port is only reachable if you publish it, so binding all
# interfaces INSIDE the container is right. It also means the daemon's bind guard
# applies: without GITSTATE_ADMIN_TOKEN (or the explicit
# GITSTATE_ADMIN_UNAUTHENTICATED=i-accept), the container exits at startup with an
# explanation rather than serving an unauthenticated management API.
ENV GITSTATE_ADDR=0.0.0.0 \
    GITSTATE_PORT=7473 \
    GITSTATE_DATA_DIR=/var/lib/gitstate

EXPOSE 7473

# The database, the enrolled peer list and this node's sync secret key all live
# here. Mount it, or the node's identity is destroyed with the container and every
# peer has to re-enrol it.
VOLUME ["/var/lib/gitstate"]

# /health is the ungated liveness route; it needs no credentials by design, which
# is exactly what makes it usable from here and from a load balancer.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${GITSTATE_PORT}/health" >/dev/null

ENTRYPOINT ["/usr/local/bin/gitstated"]
