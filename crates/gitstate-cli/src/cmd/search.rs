//! `gitstate search <query>` — hybrid full-text + semantic + fuzzy search
//! across issues, PRs, and commits (T11 port plan, wave 4 — `store/search.go`
//! + `store/embeddings.go` + `internal/embed`).
//!
//! There was no standalone Go binary for this surface the way agent_runs/
//! context_bundle had `gittrack`: Go's only caller was `/api/search` on the
//! now-deleted multi-tenant HTTP server (`internal/api`, removed as
//! SaaS-only in the 2026-08-04 sweep) and the MCP `search_issues` tool
//! (`cmd/gitstate-mcp`, which called that same HTTP route). This CLI command
//! and the `search_issues` MCP tool (`cmd::mcp`) are search's only two
//! surfaces here, both calling `gitstate_daemon::ops::search` in-process —
//! no daemon route, same evidence-based call waves 2/3 made (nothing in
//! `web/` consumes search either).

use clap::Args;

use gitstate_core::SearchKind;
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Args)]
pub struct SearchArgs {
    /// The search query.
    pub query: String,
    /// Restrict to one or more kinds (repeatable): issue(s), pr(s),
    /// commit(s). Omit for all three.
    #[arg(long = "type", value_name = "KIND")]
    pub kinds: Vec<String>,
    /// Max results to return (server-capped at 100).
    #[arg(long)]
    pub limit: Option<u32>,
}

pub fn run(ctx: &Ctx, args: SearchArgs) -> anyhow::Result<()> {
    let state = ctx.state()?;
    let kinds: Vec<SearchKind> = args
        .kinds
        .iter()
        .filter_map(|s| SearchKind::parse(s))
        .collect();
    let outcome = ops::search(&state, &args.query, &kinds, args.limit.unwrap_or(0))?;

    if ctx.json {
        return ctx.print_json(&outcome);
    }

    if outcome.results.is_empty() {
        println!("no results for \"{}\"", args.query);
        return Ok(());
    }
    let mode = if outcome.semantic {
        "semantic+fts"
    } else if outcome.fuzzy {
        "fuzzy"
    } else {
        "fts"
    };
    println!("{} result(s) [{mode}]", outcome.results.len());
    for r in &outcome.results {
        let num = r.number.map(|n| format!("#{n} ")).unwrap_or_default();
        println!("  [{:<7}] {}{}", r.kind.as_str(), num, r.title);
        if !r.snippet.is_empty() {
            println!("            {}", r.snippet);
        }
    }
    Ok(())
}
