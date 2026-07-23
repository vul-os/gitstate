//! `gitstated` — the headless daemon binary.
//!
//! Reads bind config from the environment (`GITSTATE_ADDR`, `GITSTATE_PORT`)
//! and serves the JSON API + `web/dist`. The CLI's `gitstate serve` command
//! wraps this same code path; running `gitstated` directly is the bare
//! always-on peer.

use std::net::SocketAddr;

use gitstate_daemon::{build_state_from_env, Daemon, DEFAULT_ADDR, DEFAULT_PORT};

#[tokio::main]
async fn main() {
    if let Err(e) = run().await {
        eprintln!("gitstated: {e}");
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let addr_str = std::env::var("GITSTATE_ADDR").unwrap_or_else(|_| DEFAULT_ADDR.to_string());
    let port: u16 = std::env::var("GITSTATE_PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(DEFAULT_PORT);
    let addr: SocketAddr = format!("{addr_str}:{port}").parse()?;

    let state = build_state_from_env()?;
    let web = state
        .web_dist
        .clone()
        .map(|p| p.display().to_string())
        .unwrap_or_else(|| "(none — API only)".to_string());
    eprintln!("gitstated listening on http://{addr}  (web: {web})");

    Daemon::new(state).serve(addr).await?;
    Ok(())
}
