# Makefile — gitstate development & build tasks (local-first desktop app)
#
# gitstate is a Rust Cargo workspace (crates/*) + a React web UI (web/) + a
# Tauri desktop shell (apps/desktop). It runs on your machine: no server, no
# Postgres, no billing cloud. The Go code under internal/ and cmd/ is kept
# in-tree for a staged port and is NOT built here.
#
# Prerequisites: Rust 1.85+ (cargo), Node 22+ (npm). Tauri targets additionally
# need the platform webview + toolchain (see tauri.app/start/prerequisites).
#
# Quick start (dev):
#   make dev            — run daemon (:7473) + Vite dev server (:5173) together
# Headless:
#   make build && ./target/release/gitstate serve
# Desktop:
#   make desktop        — build the Tauri app (bundles web/ + the daemon)

.PHONY: help dev dev-api dev-web build build-web build-cli run serve \
        test lint fmt fmt-check clippy sync sync-dmtap desktop desktop-dev clean

# ─── Config ──────────────────────────────────────────────────────────────────

WEB      := web
DESKTOP  := apps/desktop
SYNC_MANIFEST := crates/gitstate-sync/Cargo.toml

help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ─── dev ─────────────────────────────────────────────────────────────────────

dev: ## Run daemon + Vite dev server together (needs `npx concurrently` or two shells).
	@echo "→ starting gitstated (:7473) and vite (:5173)"
	@$(MAKE) -j2 dev-api dev-web

dev-api: ## Run the headless daemon in dev (serves the JSON API on :7473).
	cargo run -p gitstate-cli -- serve

dev-web: ## Run the Vite dev server (proxies /api + /health to :7473).
	cd $(WEB) && npm install && npm run dev

# ─── build ───────────────────────────────────────────────────────────────────

build: build-web build-cli ## Build the web app and the release binaries.
	@echo "✓ gitstate (release) + web/dist ready"

build-web: ## Build the React app into web/dist (served by the daemon + Tauri).
	cd $(WEB) && npm install && npm run build

build-cli: ## Build the release binaries: `gitstate` (CLI) + `gitstated` (daemon).
	cargo build --release -p gitstate-cli -p gitstate-daemon

run: serve ## Alias for `serve`.
serve: ## Run the headless daemon (release) serving web/dist + the API.
	cargo run --release -p gitstate-cli -- serve

# ─── quality ─────────────────────────────────────────────────────────────────

test: ## Run the workspace test suite (sync crate is excluded — see `make sync`).
	cargo test --workspace

lint: clippy ## Alias for clippy.
clippy: ## Clippy over the default workspace.
	cargo clippy --workspace --all-targets

fmt: ## Format all Rust code.
	cargo fmt --all

fmt-check: ## Check formatting without writing.
	cargo fmt --all --check

# ─── excluded sync crate (optional P2P) ──────────────────────────────────────

sync: ## Build the excluded CRDT sync crate standalone (local-only, offline).
	cargo build --manifest-path $(SYNC_MANIFEST)

sync-dmtap: ## Build the sync crate with the DMTAP transport (fetches envoir).
	cargo build --manifest-path $(SYNC_MANIFEST) --features sync-dmtap

# ─── desktop (Tauri) ─────────────────────────────────────────────────────────

desktop: ## Build the Tauri desktop app (bundles web/ and starts the daemon).
	cd $(DESKTOP) && npm install && npm run tauri build

desktop-dev: ## Run the Tauri app in dev (hot-reloads the web UI).
	cd $(DESKTOP) && npm install && npm run tauri dev

# ─── clean ───────────────────────────────────────────────────────────────────

clean: ## Remove Rust build output and the web bundle.
	cargo clean
	rm -rf $(WEB)/dist
