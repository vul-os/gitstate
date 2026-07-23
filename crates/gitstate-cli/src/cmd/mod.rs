//! CLI command implementations. Each submodule handles one command group and
//! calls into `gitstate_daemon::ops` — the same operations the HTTP API uses.

pub mod category;
pub mod context;
pub mod insight;
pub mod misc;
pub mod repo;
pub mod seed;
pub mod serve;

use gitstate_daemon::{build_state_from_env, AppState};

/// Shared invocation context (global flags).
pub struct Ctx {
    pub json: bool,
}

impl Ctx {
    /// Build the domain state (opens the DB, wires classifier + taxonomy).
    pub fn state(&self) -> anyhow::Result<AppState> {
        Ok(build_state_from_env()?)
    }

    /// Print a serializable value as pretty JSON.
    pub fn print_json<T: serde::Serialize>(&self, value: &T) -> anyhow::Result<()> {
        println!("{}", serde_json::to_string_pretty(value)?);
        Ok(())
    }
}
