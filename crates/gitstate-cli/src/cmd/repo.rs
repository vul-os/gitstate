//! `gitstate repo …` — register, list, remove, scan.

use clap::Subcommand;

use gitstate_core::RepoId;
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum RepoCmd {
    /// Register a repo by local path or remote URL.
    Add {
        /// A local worktree path, or a remote URL (https/ssh).
        target: String,
    },
    /// List registered repos.
    List,
    /// Remove a registered repo (and its caches).
    Rm { id: String },
    /// Walk git (+ forge unless --no-forge) and derive caches.
    Scan {
        /// Repo id, or omit with --all.
        id: Option<String>,
        /// Scan every registered repo.
        #[arg(long)]
        all: bool,
        /// Skip the forge snapshot (git only).
        #[arg(long)]
        no_forge: bool,
        /// Only include history since this RFC3339 instant.
        #[arg(long)]
        since: Option<String>,
    },
}

pub async fn run(ctx: &Ctx, cmd: RepoCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        RepoCmd::Add { target } => {
            let (path, remote) = if looks_like_url(&target) {
                (None, Some(target))
            } else {
                (Some(target), None)
            };
            let repo = ops::add_repo(&state, path, remote)?;
            if ctx.json {
                ctx.print_json(&repo)?;
            } else {
                println!(
                    "added {}  {}  [{}]",
                    repo.id,
                    repo.slug,
                    repo.forge.as_str()
                );
            }
        }
        RepoCmd::List => {
            let repos = ops::list_repos(&state)?;
            if ctx.json {
                ctx.print_json(&repos)?;
            } else if repos.is_empty() {
                println!("no repos registered (try: gitstate repo add <path>)");
            } else {
                for r in &repos {
                    println!(
                        "{}  {:<28} {:<7} {}",
                        r.id,
                        r.slug,
                        r.forge.as_str(),
                        r.last_scanned_at.as_deref().unwrap_or("never scanned")
                    );
                }
            }
        }
        RepoCmd::Rm { id } => {
            ops::delete_repo(&state, &RepoId::from(id.clone()))?;
            println!("removed {id}");
        }
        RepoCmd::Scan {
            id,
            all,
            no_forge,
            since,
        } => {
            let targets: Vec<RepoId> = if all {
                ops::list_repos(&state)?.into_iter().map(|r| r.id).collect()
            } else if let Some(id) = id {
                vec![RepoId::from(id)]
            } else {
                anyhow::bail!("provide a repo id or --all");
            };
            let mut results = Vec::new();
            for rid in targets {
                let res = ops::scan_repo(&state, &rid, !no_forge, since.clone()).await?;
                if !ctx.json {
                    println!(
                        "scanned {}  head={}  commits={}  contributors={}  work_items={}",
                        res.repo_id,
                        short(&res.head_sha),
                        res.commits_scanned,
                        res.contributors,
                        res.work_items
                    );
                    for w in &res.warnings {
                        println!("  warning: {w}");
                    }
                }
                results.push(res);
            }
            if ctx.json {
                ctx.print_json(&results)?;
            }
        }
    }
    Ok(())
}

fn looks_like_url(s: &str) -> bool {
    s.contains("://")
        || s.starts_with("git@")
        || (s.contains(':') && !std::path::Path::new(s).exists())
}

fn short(sha: &str) -> &str {
    if sha.len() >= 8 {
        &sha[..8]
    } else {
        sha
    }
}
