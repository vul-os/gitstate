//! `gitstate taxonomy | sync | data`.

use std::path::PathBuf;

use clap::Subcommand;

use gitstate_core::{Hlc, Taxonomy};
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum TaxonomyCmd {
    /// Print the active signed taxonomy.
    Show,
    /// Verify a taxonomy's signature against the pinned key.
    Verify {
        /// A taxonomy JSON file. Default: the active (embedded/loaded) doc.
        #[arg(long)]
        file: Option<PathBuf>,
    },
}

#[derive(Debug, Subcommand)]
pub enum SyncCmd {
    /// Show sync status.
    Status,
    /// Publish local ops to peers (needs the `sync-dmtap` build).
    Publish {
        /// Only publish ops after this HLC (JSON).
        #[arg(long)]
        since: Option<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum DataCmd {
    /// Print the resolved data dir + database path.
    Path,
}

pub fn taxonomy(ctx: &Ctx, cmd: TaxonomyCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        TaxonomyCmd::Show => {
            let tx = ops::taxonomy(&state);
            if ctx.json {
                ctx.print_json(&tx)?;
            } else {
                println!("schema   {}", tx.schema);
                println!("version  {}", tx.version);
                println!("id       {}", tx.id);
                println!("issued   {}", tx.issued_at);
                println!("pubkey   {}", tx.pubkey);
                println!("categories:");
                for c in &tx.categories {
                    println!(
                        "  {:<16} {}{}",
                        c.key,
                        c.label,
                        c.parent
                            .as_deref()
                            .map(|p| format!("  ({p})"))
                            .unwrap_or_default()
                    );
                }
            }
        }
        TaxonomyCmd::Verify { file } => {
            let tx = match file {
                Some(path) => {
                    let bytes = std::fs::read(&path)?;
                    serde_json::from_slice::<Taxonomy>(&bytes)?
                }
                None => ops::taxonomy(&state),
            };
            match ops::verify_taxonomy(&tx) {
                Ok(id) => println!("valid   id={id}"),
                Err(e) => {
                    anyhow::bail!("invalid taxonomy: {e}");
                }
            }
        }
    }
    Ok(())
}

pub async fn sync(ctx: &Ctx, cmd: SyncCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        SyncCmd::Status => {
            let st = ops::sync_status(&state).await?;
            if ctx.json {
                ctx.print_json(&st)?;
            } else {
                println!("enabled  {}", st.enabled);
                println!("peer_id  {}", st.peer_id);
                println!("peers    {}", st.peers);
            }
        }
        SyncCmd::Publish { since } => {
            let hlc = match since {
                Some(s) => Some(Hlc::decode(&s)?),
                None => None,
            };
            match ops::sync_publish(&state, hlc).await {
                Ok(n) => println!("published {n} ops"),
                Err(e) => anyhow::bail!("{e}"),
            }
        }
    }
    Ok(())
}

pub fn data(ctx: &Ctx, cmd: DataCmd) -> anyhow::Result<()> {
    match cmd {
        DataCmd::Path => {
            let (dir, db) = ops::data_paths()?;
            if ctx.json {
                ctx.print_json(&serde_json::json!({
                    "data_dir": dir.display().to_string(),
                    "db_path": db.display().to_string(),
                }))?;
            } else {
                println!("data_dir  {}", dir.display());
                println!("db_path   {}", db.display());
            }
        }
    }
    Ok(())
}
