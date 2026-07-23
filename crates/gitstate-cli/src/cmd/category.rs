//! `gitstate category …` — list, add, remove.

use clap::Subcommand;

use gitstate_daemon::dto::NewCategory;
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum CategoryCmd {
    /// List categories (taxonomy + local + peer).
    List,
    /// Add a local category.
    Add {
        #[arg(long)]
        key: String,
        #[arg(long)]
        label: String,
        #[arg(long)]
        parent: Option<String>,
        #[arg(long)]
        color: Option<String>,
    },
    /// Delete a category (tombstone).
    Rm { id: String },
}

pub fn run(ctx: &Ctx, cmd: CategoryCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        CategoryCmd::List => {
            let rows = ops::list_categories(&state)?;
            if ctx.json {
                ctx.print_json(&rows)?;
            } else if rows.is_empty() {
                println!("no categories");
            } else {
                for c in &rows {
                    println!(
                        "{}  {:<18} {:<24} [{}]{}",
                        c.id,
                        c.key,
                        c.label,
                        c.source.as_str(),
                        c.parent_key
                            .as_deref()
                            .map(|p| format!("  parent={p}"))
                            .unwrap_or_default()
                    );
                }
            }
        }
        CategoryCmd::Add {
            key,
            label,
            parent,
            color,
        } => {
            let req = NewCategory {
                key,
                label,
                parent_key: parent,
                color,
            };
            let c = ops::create_category(&state, req)?;
            if ctx.json {
                ctx.print_json(&c)?;
            } else {
                println!("added {}  {}", c.id, c.key);
            }
        }
        CategoryCmd::Rm { id } => {
            ops::delete_category(&state, &id)?;
            println!("removed {id}");
        }
    }
    Ok(())
}
