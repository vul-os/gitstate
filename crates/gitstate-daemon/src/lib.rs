//! gitstate-daemon — the headless, always-on local peer.
//!
//! An axum server that serves the built React app (`web/dist`) at `/` with SPA
//! fallback, and the full JSON API under `/api` (see `docs/ARCHITECTURE.md`
//! §3). The same server backs both the headless CLI (`gitstate serve`) and the
//! Tauri desktop shell — the desktop app starts it in-process on an ephemeral
//! port and points the webview at it, so one API surface serves both modes.
//!
//! All domain logic lives in the [`ops`] module (shared verbatim with the
//! CLI); the route handlers are thin JSON adapters over it. Binds loopback by
//! default; no telemetry, no uninvited network calls (a plain scan of a
//! `Local` repo never touches a forge).

pub mod dto;
pub mod ops;
mod router;
mod routes;
pub mod serve_static;
pub mod state;

pub use state::{build_state_from_env, AppState, Daemon, ForgeRegistry};

/// Default daemon port (env `GITSTATE_PORT`).
pub const DEFAULT_PORT: u16 = 7473;
/// Default bind address (env `GITSTATE_ADDR`).
pub const DEFAULT_ADDR: &str = "127.0.0.1";
