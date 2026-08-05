# Makefile — gitstate development & build tasks (local-first desktop app)
#
# gitstate is a Rust Cargo workspace (crates/*) + a React web UI (web/) + a
# Tauri desktop shell (apps/desktop). It runs on your machine: no server, no
# Postgres, no billing cloud. The legacy Go+Postgres implementation this was
# ported from is gone — see docs/MIGRATION-NOTES.md.
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
        test lint fmt fmt-check clippy offline-check desktop desktop-dev clean

# ─── Config ──────────────────────────────────────────────────────────────────

WEB      := web
DESKTOP  := apps/desktop

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

test: ## Run the whole workspace suite, including sync convergence + engine parity.
	cargo test --workspace

lint: clippy ## Alias for clippy.
clippy: ## Clippy over the default workspace.
	cargo clippy --workspace --all-targets

fmt: ## Format all Rust code.
	cargo fmt --all

fmt-check: ## Check formatting without writing.
	cargo fmt --all --check

# ─── build invariants ────────────────────────────────────────────────────────
#
# gitstate-sync used to be excluded from the workspace behind a `sync-dmtap`
# feature, so it needed its own targets here. It no longer is: the shared merge
# engine comes from crates.io (`kotva-sync`, dev-only) instead of a git dependency
# on the envoir *product*, so `make build` and `make test` cover it.
#
# What still needs asserting is the invariant that replaced the exclusion.

offline-check: ## Assert a bare build needs no network and no other product's repo.
	cargo build --workspace --locked --offline
	@if grep -q 'source = "git' Cargo.lock; then \
	  echo "FAIL: Cargo.lock has a git source — gitstate must depend on the substrate,"; \
	  echo "      published to crates.io, never on another product's repository:"; \
	  grep -n 'source = "git' Cargo.lock; \
	  exit 1; \
	fi
	@echo "ok: offline --locked build succeeds and Cargo.lock has no git sources"

# ─── desktop (Tauri) ─────────────────────────────────────────────────────────

desktop: ## Build the Tauri desktop app (bundles web/ and starts the daemon).
	cd $(DESKTOP) && npm install && npm run tauri build

desktop-dev: ## Run the Tauri app in dev (hot-reloads the web UI).
	cd $(DESKTOP) && npm install && npm run tauri dev

# ─── clean ───────────────────────────────────────────────────────────────────

clean: ## Remove Rust build output and the web bundle.
	cargo clean
	rm -rf $(WEB)/dist
