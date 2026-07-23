//! gitstate — the command-line interface.
//!
//! `gitstate serve` runs the headless daemon (the always-on local peer); every
//! other command drives the same domain operations the daemon exposes over
//! HTTP (see `gitstate_daemon::ops`), so the CLI and the API share one code
//! path. Human-readable output by default; `--json` for machines.

mod cmd;

use std::path::PathBuf;

use clap::{Parser, Subcommand};

use cmd::{context::ContextCmd, Ctx};

#[derive(Debug, Parser)]
#[command(
    name = "gitstate",
    version,
    about = "Derive true project state, effort, contribution, and classification \
             directly from your git + forge — locally."
)]
struct Cli {
    /// Data directory (DB + caches). Env: GITSTATE_DATA_DIR.
    #[arg(long, global = true, env = "GITSTATE_DATA_DIR")]
    data_dir: Option<PathBuf>,

    /// Machine-readable JSON output where applicable.
    #[arg(long, global = true)]
    json: bool,

    #[command(subcommand)]
    command: Command,
}

#[derive(Debug, Subcommand)]
enum Command {
    /// Run the headless daemon (serves web/dist + the JSON API).
    Serve(cmd::serve::ServeArgs),

    /// Manage registered repositories.
    #[command(subcommand)]
    Repo(cmd::repo::RepoCmd),

    /// Print a repo's derived ProjectState.
    State { repo_id: String },

    /// Print the six-dimension contribution table for a repo.
    Contributions(cmd::insight::ContribArgs),

    /// List merged contributor identities.
    Contributors,

    /// Classify a repo's work items.
    Classify(cmd::insight::ClassifyArgs),

    /// Judge diff-difficulty (effort) for a repo's work items.
    Effort(cmd::insight::ClassifyArgs),

    /// Manage saved working sets (contexts).
    #[command(subcommand)]
    Context(ContextCmd),

    /// Manage categories.
    #[command(subcommand)]
    Category(cmd::category::CategoryCmd),

    /// Inspect the signed taxonomy.
    #[command(subcommand)]
    Taxonomy(cmd::misc::TaxonomyCmd),

    /// Peer-to-peer sync status/publish (needs the `sync-dmtap` build).
    #[command(subcommand)]
    Sync(cmd::misc::SyncCmd),

    /// Show resolved data + database paths.
    #[command(subcommand)]
    Data(cmd::misc::DataCmd),
}

#[tokio::main]
async fn main() {
    if let Err(e) = run().await {
        eprintln!("gitstate: {e:#}");
        std::process::exit(1);
    }
}

async fn run() -> anyhow::Result<()> {
    let cli = Cli::parse();
    if let Some(dir) = &cli.data_dir {
        std::env::set_var("GITSTATE_DATA_DIR", dir);
    }
    let ctx = Ctx { json: cli.json };

    match cli.command {
        Command::Serve(args) => cmd::serve::run(args).await,
        Command::Repo(c) => cmd::repo::run(&ctx, c).await,
        Command::State { repo_id } => cmd::insight::state(&ctx, &repo_id),
        Command::Contributions(a) => cmd::insight::contributions(&ctx, a),
        Command::Contributors => cmd::insight::contributors(&ctx),
        Command::Classify(a) => cmd::insight::classify(&ctx, a).await,
        Command::Effort(a) => cmd::insight::effort(&ctx, a).await,
        Command::Context(c) => cmd::context::run(&ctx, c),
        Command::Category(c) => cmd::category::run(&ctx, c),
        Command::Taxonomy(c) => cmd::misc::taxonomy(&ctx, c),
        Command::Sync(c) => cmd::misc::sync(&ctx, c).await,
        Command::Data(c) => cmd::misc::data(&ctx, c),
    }
}
