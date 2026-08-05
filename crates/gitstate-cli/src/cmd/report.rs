//! `gitstate report burndown | activity | status | ask` — the dashboard's
//! missing pieces plus NL→report, ported from Go's `internal/report`
//! (`report.go`), the last of T11's five port-plan domains.
//!
//! Most of the dashboard's rollup math (throughput, cycle-time trend, state
//! counts) already existed before this wave in `gitstate_core::analytics`,
//! served at `GET /api/analytics` and rendered by `web/`'s Dashboard/Insights
//! pages. Only burndown, the recent-activity feed, LLM status synthesis, and
//! NL→report were genuinely missing — see `gitstate_daemon::ops`'s "report"
//! section and `gitstate_report::nl`'s module doc (the latter for the
//! NL→report security redesign specifically).
//!
//! CLI-only, no daemon HTTP route: `web/` has no consumer for any of these
//! four (checked), so there is no `/api/*` contract to keep — the same
//! evidence-based call waves 2/3/4 made for their own domains.

use clap::{Args, Subcommand};

use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum ReportCmd {
    /// Daily open-vs-total issue count over a trailing window.
    Burndown(WindowArgs),
    /// The most recent issues/PRs/commits, newest first.
    Activity {
        #[arg(long)]
        repo: Option<String>,
        /// Max items to show (default 20).
        #[arg(long)]
        limit: Option<u32>,
    },
    /// A leadership-readable prose status paragraph over recent activity.
    /// Requires an LLM endpoint (`VULOS_LLMUX_URL`/`OPENAI_BASE_URL`); prints
    /// nothing (not an error) if none is configured.
    Status {
        #[arg(long)]
        repo: Option<String>,
        /// How many recent items to summarize (default 20).
        #[arg(long)]
        limit: Option<u32>,
    },
    /// Ask a natural-language question, answered by dispatching to one of a
    /// fixed set of report intents — never by generating SQL. Requires an
    /// LLM endpoint. See `gitstate_report::nl` for what this can and cannot
    /// answer, and why.
    Ask {
        /// The question, e.g. "how many issues are open" or "what merged
        /// this week".
        question: String,
    },
}

#[derive(Debug, Args)]
pub struct WindowArgs {
    #[arg(long)]
    pub repo: Option<String>,
    /// Trailing window size in days (default 30).
    #[arg(long)]
    pub days: Option<u32>,
}

pub async fn run(ctx: &Ctx, cmd: ReportCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        ReportCmd::Burndown(args) => {
            let repo = resolve_optional_repo(&state, args.repo.as_deref())?;
            let series = ops::burndown(&state, repo.as_ref(), args.days)?;
            if ctx.json {
                ctx.print_json(&series)?;
            } else if series.is_empty() {
                println!("no burndown data (add and scan a repo first)");
            } else {
                println!("{:<12} {:>8} {:>8}", "date", "open", "total");
                for p in &series {
                    println!("{:<12} {:>8} {:>8}", p.date, p.open, p.total);
                }
            }
        }
        ReportCmd::Activity { repo, limit } => {
            let repo = resolve_optional_repo(&state, repo.as_deref())?;
            let items = ops::recent_activity(&state, repo.as_ref(), limit.unwrap_or(20))?;
            if ctx.json {
                ctx.print_json(&items)?;
            } else if items.is_empty() {
                println!("no recent activity");
            } else {
                for a in &items {
                    let state_part = if a.state.is_empty() {
                        String::new()
                    } else {
                        format!(" [{}]", a.state)
                    };
                    let author_part = if a.author.is_empty() {
                        String::new()
                    } else {
                        format!(" (by {})", a.author)
                    };
                    println!(
                        "{:<8} {}{}{}  {}",
                        a.kind, a.title, state_part, author_part, a.at
                    );
                }
            }
        }
        ReportCmd::Status { repo, limit } => {
            let repo = resolve_optional_repo(&state, repo.as_deref())?;
            match ops::status_synthesis(&state, repo.as_ref(), limit.unwrap_or(20)).await? {
                Some(text) => {
                    if ctx.json {
                        ctx.print_json(&serde_json::json!({ "status": text }))?;
                    } else {
                        println!("{text}");
                    }
                }
                None => {
                    if ctx.json {
                        ctx.print_json(&serde_json::json!({ "status": null }))?;
                    } else {
                        println!(
                            "no LLM endpoint configured (set VULOS_LLMUX_URL or OPENAI_BASE_URL) \
                             or no recent activity to summarize"
                        );
                    }
                }
            }
        }
        ReportCmd::Ask { question } => {
            let answer = ops::ask_report(&state, &question).await?;
            if ctx.json {
                ctx.print_json(&answer)?;
            } else {
                println!("intent: {:?}", answer.intent);
                if let Some(prose) = &answer.prose {
                    println!("\n{prose}");
                } else {
                    println!("\n{}", answer.data);
                }
            }
        }
    }
    Ok(())
}

/// Like [`super::resolve_repo`] but for an *optional* repo argument (report
/// commands default to "every repo" when none is given, unlike commands that
/// operate on exactly one repo).
fn resolve_optional_repo(
    state: &gitstate_daemon::AppState,
    needle: Option<&str>,
) -> anyhow::Result<Option<gitstate_core::RepoId>> {
    match needle {
        Some(n) => Ok(Some(super::resolve_repo(state, n)?)),
        None => Ok(None),
    }
}
