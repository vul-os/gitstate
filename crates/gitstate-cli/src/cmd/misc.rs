//! `gitstate taxonomy | sync | data`.

use std::path::PathBuf;

use clap::Subcommand;

use gitstate_core::{Hlc, PeerId, SyncPeer, Taxonomy};
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
    /// Show sync status: this node's id and how many peers are enrolled.
    Status,
    /// Print this node's peer id and public key — the two values you give the
    /// OTHER node so it can enrol this one.
    Identity,
    /// Record local ops in the op log (the local half; no network).
    Publish {
        /// Only publish ops after this HLC (JSON).
        #[arg(long)]
        since: Option<String>,
    },
    /// Manage the peer list. Enrolment is manual and there is no discovery: you
    /// supply the URL and the key, both out of band.
    #[command(subcommand)]
    Peer(PeerCmd),
    /// Run one replication round with every enrolled peer (push, then pull).
    Run,
}

#[derive(Debug, Subcommand)]
pub enum PeerCmd {
    /// List enrolled peers.
    List,
    /// Enrol a peer by the address and key its operator gave you.
    Add {
        /// The peer's id, from its `gitstate sync identity`.
        #[arg(long)]
        id: String,
        /// The peer's base URL, e.g. https://gitstate.example.org
        #[arg(long)]
        url: String,
        /// The peer's hex ed25519 public key, from its `gitstate sync identity`.
        #[arg(long)]
        key: String,
        /// An optional label for humans.
        #[arg(long)]
        label: Option<String>,
    },
    /// Remove an enrolment. That peer's ops are no longer admitted.
    Remove {
        #[arg(long)]
        id: String,
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
        SyncCmd::Identity => {
            let id = ops::node_identity_view(&state)?;
            if ctx.json {
                ctx.print_json(&serde_json::json!({
                    "peer_id": id.peer_id, "pubkey": id.pubkey,
                }))?;
            } else {
                println!("peer_id  {}", id.peer_id);
                println!("pubkey   {}", id.pubkey);
                println!();
                println!("Give both to the other node's operator, who runs:");
                println!(
                    "  gitstate sync peer add --id {} --key {} --url <this node's url>",
                    id.peer_id, id.pubkey
                );
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
        SyncCmd::Peer(peer_cmd) => return sync_peer(ctx, peer_cmd),
        SyncCmd::Run => {
            let report = ops::sync_run(&state).await?;
            if ctx.json {
                ctx.print_json(&report)?;
            } else {
                for p in &report.peers {
                    match &p.error {
                        Some(e) => println!("{}  {}  FAILED: {e}", p.peer_id, p.url),
                        None => println!(
                            "{}  {}  pushed={} pulled={} applied={} skipped={} rejected={}",
                            p.peer_id, p.url, p.pushed, p.pulled, p.applied, p.skipped, p.rejected
                        ),
                    }
                }
                // A rejection is not noise: it means a peer offered something
                // that failed verification. Say so where an operator will see it.
                let rejected: u32 = report.peers.iter().map(|p| p.rejected).sum();
                if rejected > 0 {
                    println!(
                        "\n{rejected} op(s) were REJECTED (bad signature, unenrolled author, \
                         forged clock identity, or a clock beyond the skew bound)."
                    );
                }
                if report.peers.iter().any(|p| p.error.is_some()) {
                    anyhow::bail!("at least one peer round failed");
                }
            }
        }
    }
    Ok(())
}

fn sync_peer(ctx: &Ctx, cmd: PeerCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        PeerCmd::List => {
            let peers = state.store.list_sync_peers()?;
            if ctx.json {
                ctx.print_json(&peers)?;
            } else if peers.is_empty() {
                println!("no peers enrolled — this node replicates with nobody.");
                println!(
                    "Enrol one with: gitstate sync peer add --id <id> --url <url> --key <hex>"
                );
            } else {
                for p in &peers {
                    println!(
                        "{}  {}  {}{}",
                        p.id,
                        p.url,
                        &p.pubkey[..16.min(p.pubkey.len())],
                        p.label
                            .as_deref()
                            .map(|l| format!("  ({l})"))
                            .unwrap_or_default()
                    );
                }
            }
        }
        PeerCmd::Add {
            id,
            url,
            key,
            label,
        } => {
            let peer = SyncPeer {
                id: PeerId::from(id.trim()),
                url: gitstate_core::normalize_peer_url(&url)?,
                pubkey: gitstate_core::normalize_pubkey(&key)?,
                label,
                added_at: gitstate_core::now_rfc3339(),
                last_pull_hlc: None,
            };
            if peer.id.0.is_empty() {
                anyhow::bail!("--id is required (see the peer's `gitstate sync identity`)");
            }
            state.store.upsert_sync_peer(&peer)?;
            println!("enrolled {}  {}", peer.id, peer.url);
        }
        PeerCmd::Remove { id } => {
            let removed = state.store.delete_sync_peer(&PeerId::from(id.trim()))?;
            if removed {
                println!("removed {id}");
            } else {
                anyhow::bail!("no peer enrolled with id {id}");
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
