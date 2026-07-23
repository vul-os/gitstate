//! Starts the in-process daemon on an ephemeral loopback port and returns its
//! base URL (e.g. `http://127.0.0.1:52341`). The serve loop runs detached on
//! Tauri's async runtime for the lifetime of the process.

use gitstate_daemon::{build_state_from_env, Daemon};

/// Boot the daemon and return its base URL. Errors are surfaced as strings so
/// the caller can `.expect` with a clear message during setup.
pub fn start() -> Result<String, String> {
    let addr = tauri::async_runtime::block_on(async {
        let state = build_state_from_env().map_err(|e| e.to_string())?;
        // `serve_ephemeral` binds 127.0.0.1:0, spawns the serve loop, and hands
        // back the chosen address. Dropping the JoinHandle does not stop the
        // task — it keeps serving on the runtime.
        let (addr, _handle) = Daemon::new(state)
            .serve_ephemeral()
            .await
            .map_err(|e| e.to_string())?;
        Ok::<_, String>(addr)
    })?;
    Ok(format!("http://{addr}"))
}
