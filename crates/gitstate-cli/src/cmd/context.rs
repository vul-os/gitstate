//! `gitstate context …` — the sharable saved working sets.

use std::path::PathBuf;

use clap::Subcommand;

use gitstate_core::{Context, ContextId};
use gitstate_daemon::dto::{ContextPatch, NewContext};
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum ContextCmd {
    /// List contexts.
    List,
    /// Show one context.
    Show { id: String },
    /// Create a context.
    Create {
        #[arg(long)]
        name: String,
        #[arg(long)]
        desc: Option<String>,
        /// Repo id (repeatable).
        #[arg(long = "repo")]
        repos: Vec<String>,
        /// PR ref as `slug#number` (repeatable).
        #[arg(long = "pr")]
        prs: Vec<String>,
        /// Tag (repeatable).
        #[arg(long = "tag")]
        tags: Vec<String>,
        #[arg(long)]
        notes: Option<String>,
    },
    /// Edit a context (add/remove members, change text).
    Edit {
        id: String,
        #[arg(long)]
        name: Option<String>,
        #[arg(long)]
        desc: Option<String>,
        #[arg(long)]
        notes: Option<String>,
        #[arg(long = "add-repo")]
        add_repo: Vec<String>,
        #[arg(long = "rm-repo")]
        rm_repo: Vec<String>,
        #[arg(long = "add-tag")]
        add_tag: Vec<String>,
        #[arg(long = "rm-tag")]
        rm_tag: Vec<String>,
    },
    /// Delete a context (tombstone).
    Rm { id: String },
    /// Export a context to a portable JSON file.
    Export {
        id: String,
        #[arg(long)]
        out: PathBuf,
    },
    /// Import a context from a JSON file (mints a fresh id).
    Import { file: PathBuf },
}

pub fn run(ctx: &Ctx, cmd: ContextCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        ContextCmd::List => {
            let rows = ops::list_contexts(&state)?;
            if ctx.json {
                ctx.print_json(&rows)?;
            } else if rows.is_empty() {
                println!("no contexts");
            } else {
                for c in &rows {
                    println!(
                        "{}  {:<28} repos={} prs={} tags={}",
                        c.id,
                        c.name,
                        c.repo_ids.len(),
                        c.pr_refs.len(),
                        c.tags.len()
                    );
                }
            }
        }
        ContextCmd::Show { id } => {
            let c = ops::get_context(&state, &ContextId::from(id))?;
            ctx.print_json(&c)?;
        }
        ContextCmd::Create {
            name,
            desc,
            repos,
            prs,
            tags,
            notes,
        } => {
            let pr_refs = prs
                .iter()
                .map(|t| ops::parse_pr_token(t))
                .collect::<Result<Vec<_>, _>>()?;
            let req = NewContext {
                name,
                description: desc,
                repo_ids: Some(repos),
                pr_refs: Some(pr_refs),
                notes,
                tags: Some(tags),
            };
            let c = ops::create_context(&state, req)?;
            if ctx.json {
                ctx.print_json(&c)?;
            } else {
                println!("created {}  {}", c.id, c.name);
            }
        }
        ContextCmd::Edit {
            id,
            name,
            desc,
            notes,
            add_repo,
            rm_repo,
            add_tag,
            rm_tag,
        } => {
            let cid = ContextId::from(id);
            let existing = ops::get_context(&state, &cid)?;
            let repo_ids = apply_set(
                existing.repo_ids.iter().map(|r| r.0.clone()).collect(),
                &add_repo,
                &rm_repo,
            );
            let tags = apply_set(existing.tags.clone(), &add_tag, &rm_tag);
            let patch = ContextPatch {
                name,
                description: desc,
                notes,
                repo_ids: Some(repo_ids),
                pr_refs: None,
                tags: Some(tags),
            };
            let c = ops::patch_context(&state, &cid, patch)?;
            if ctx.json {
                ctx.print_json(&c)?;
            } else {
                println!("updated {}", c.id);
            }
        }
        ContextCmd::Rm { id } => {
            ops::delete_context(&state, &ContextId::from(id.clone()))?;
            println!("removed {id}");
        }
        ContextCmd::Export { id, out } => {
            let c = ops::get_context(&state, &ContextId::from(id))?;
            std::fs::write(&out, serde_json::to_string_pretty(&c)?)?;
            println!("exported {} -> {}", c.id, out.display());
        }
        ContextCmd::Import { file } => {
            let bytes = std::fs::read(&file)?;
            let parsed: Context = serde_json::from_slice(&bytes)?;
            let c = ops::import_context(&state, parsed)?;
            if ctx.json {
                ctx.print_json(&c)?;
            } else {
                println!("imported as {}  {}", c.id, c.name);
            }
        }
    }
    Ok(())
}

/// Apply add/remove edits to a set, preserving order and de-duplicating.
fn apply_set(mut base: Vec<String>, add: &[String], rm: &[String]) -> Vec<String> {
    base.retain(|x| !rm.contains(x));
    for a in add {
        if !base.contains(a) {
            base.push(a.clone());
        }
    }
    base
}
