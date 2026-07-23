//! `gitstate serve` — run the headless daemon.

use std::net::SocketAddr;
use std::path::PathBuf;

use clap::Args;

use gitstate_daemon::{build_state_from_env, Daemon, DEFAULT_ADDR, DEFAULT_PORT};

#[derive(Debug, Args)]
pub struct ServeArgs {
    /// Bind address. Env: GITSTATE_ADDR.
    #[arg(long, env = "GITSTATE_ADDR", default_value = DEFAULT_ADDR)]
    addr: String,

    /// Bind port. Env: GITSTATE_PORT.
    #[arg(long, env = "GITSTATE_PORT", default_value_t = DEFAULT_PORT)]
    port: u16,

    /// Path to the built web app (web/dist). Env: GITSTATE_WEB_DIST.
    #[arg(long, env = "GITSTATE_WEB_DIST")]
    web_dist: Option<PathBuf>,
}

pub async fn run(args: ServeArgs) -> anyhow::Result<()> {
    if let Some(dist) = &args.web_dist {
        std::env::set_var("GITSTATE_WEB_DIST", dist);
    }
    let addr: SocketAddr = format!("{}:{}", args.addr, args.port).parse()?;
    let state = build_state_from_env()?;
    let web = state
        .web_dist
        .clone()
        .map(|p| p.display().to_string())
        .unwrap_or_else(|| "(none — API only)".to_string());
    eprintln!("gitstate serve: http://{addr}  (web: {web})");
    Daemon::new(state).serve(addr).await?;
    Ok(())
}
