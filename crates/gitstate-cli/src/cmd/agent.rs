//! `gitstate agent log-run | runs | whoami` — the agent-native surface Go's
//! `gittrack` shipped as a standalone binary (`log-run`/`runs`/`whoami`), now
//! folded into `gitstate-cli` (T11 port plan, wave 1). `gittrack context`/
//! `pr`/`issues` are NOT here: those are the `context_bundle` domain, a later
//! wave — this file only carries what `store/agent_runs.go` covers.
//!
//! Like every other `gitstate` subcommand, these call `gitstate_daemon::ops`
//! directly against the local SQLite file — no HTTP, no token. `gittrack` was
//! a separate binary because it spoke to a *remote*, multi-tenant Postgres
//! server over `GITSTATE_URL`/`GITSTATE_TOKEN`; there is no separate server
//! to reach here, so there is nothing left for a bearer token to gate. See
//! `whoami` below for what replaces token validation in a single-user app.

use clap::Subcommand;

use gitstate_core::AgentDiffSummary;
use gitstate_daemon::dto::{AgentRunQuery, NewAgentRun};
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Subcommand)]
pub enum AgentCmd {
    /// Record an agent run so it feeds attribution + estimation.
    LogRun {
        /// What the agent set out to do (required).
        #[arg(long)]
        goal: String,
        /// Repo id this run worked on.
        #[arg(long)]
        repo: Option<String>,
        /// Pull request id this run produced.
        #[arg(long)]
        pr: Option<String>,
        /// Issue id this run addressed.
        #[arg(long)]
        issue: Option<String>,
        /// Agent name/model (e.g. claude-code, cursor).
        #[arg(long, default_value = "gitstate")]
        agent: String,
        /// Branch the run worked on.
        #[arg(long)]
        branch: Option<String>,
        /// Human verdict: accepted | edited | reverted.
        #[arg(long)]
        action: Option<String>,
        /// Number of agent iterations.
        #[arg(long)]
        iterations: Option<u32>,
        /// Run cost in USD.
        #[arg(long)]
        cost: Option<f64>,
        /// Lines added (folded into the diff summary).
        #[arg(long)]
        additions: Option<u32>,
        /// Lines deleted (folded into the diff summary).
        #[arg(long)]
        deletions: Option<u32>,
        /// Files changed (folded into the diff summary).
        #[arg(long)]
        files: Option<u32>,
        /// Mark that the agent's own tests passed.
        #[arg(long)]
        tests_passed: bool,
    },
    /// List logged agent runs, newest-first.
    Runs {
        #[arg(long)]
        repo: Option<String>,
        #[arg(long)]
        pr: Option<String>,
        #[arg(long)]
        issue: Option<String>,
        #[arg(long)]
        agent: Option<String>,
        #[arg(long)]
        limit: Option<u32>,
    },
    /// Print this node's identity and how its management API is gated.
    ///
    /// Go's `gittrack whoami` validated a `GITSTATE_TOKEN` against a remote,
    /// multi-tenant server and printed the URL it resolved to — proof "this
    /// token still works and reaches that org". There is no token and no
    /// remote server here: the equivalent question for a single-user local
    /// app is "what is this install, and would a network caller need
    /// anything to reach it" — this prints this node's sync identity plus
    /// the resolved `AdminAuth` posture (see `gitstate_daemon::state`) so an
    /// agent host can tell at a glance whether it would need a bearer token
    /// if it ever spoke to this daemon over HTTP instead of in-process.
    Whoami,
}

pub async fn run(ctx: &Ctx, cmd: AgentCmd) -> anyhow::Result<()> {
    let state = ctx.state()?;
    match cmd {
        AgentCmd::LogRun {
            goal,
            repo,
            pr,
            issue,
            agent,
            branch,
            action,
            iterations,
            cost,
            additions,
            deletions,
            files,
            tests_passed,
        } => {
            let diff_summary = if additions.is_some() || deletions.is_some() || files.is_some() {
                Some(AgentDiffSummary {
                    additions: additions.unwrap_or(0),
                    deletions: deletions.unwrap_or(0),
                    changed_files: files.unwrap_or(0),
                })
            } else {
                None
            };
            let req = NewAgentRun {
                goal,
                repo_id: repo,
                pr_id: pr,
                issue_id: issue,
                supervisor_id: None,
                agent_name: Some(agent),
                branch,
                diff_summary,
                tests_passed: tests_passed.then_some(true),
                human_action: action,
                iterations,
                cost_usd: cost,
            };
            let run = ops::log_agent_run(&state, req)?;
            if ctx.json {
                ctx.print_json(&run)?;
            } else {
                println!("logged agent run {}", run.id);
            }
        }
        AgentCmd::Runs {
            repo,
            pr,
            issue,
            agent,
            limit,
        } => {
            let q = AgentRunQuery {
                repo_id: repo,
                pr_id: pr,
                issue_id: issue,
                agent_name: agent,
                limit,
            };
            let runs = ops::list_agent_runs(&state, q)?;
            if ctx.json {
                ctx.print_json(&runs)?;
            } else if runs.is_empty() {
                println!("no agent runs logged");
            } else {
                println!("{:<38} {:<20} {:<12} {:<9} goal", "id", "agent", "action", "tests");
                for r in &runs {
                    let action = r.human_action.map(|a| a.as_str()).unwrap_or("-");
                    let tests = match r.tests_passed {
                        Some(true) => "pass",
                        Some(false) => "fail",
                        None => "-",
                    };
                    println!(
                        "{:<38} {:<20} {:<12} {:<9} {}",
                        r.id,
                        r.agent_name.as_deref().unwrap_or("-"),
                        action,
                        tests,
                        r.goal
                    );
                }
            }
        }
        AgentCmd::Whoami => {
            let identity = ops::node_identity_view(&state)?;
            let (data_dir, db_path) = ops::data_paths()?;
            let posture = describe_admin_auth(&state.admin_auth);
            if ctx.json {
                ctx.print_json(&serde_json::json!({
                    "peer_id": identity.peer_id,
                    "pubkey": identity.pubkey,
                    "data_dir": data_dir.display().to_string(),
                    "db_path": db_path.display().to_string(),
                    "admin_auth": posture,
                }))?;
            } else {
                println!("OK — local node");
                println!("peer_id     {}", identity.peer_id);
                println!("db_path     {}", db_path.display());
                println!("admin_auth  {posture}");
            }
        }
    }
    Ok(())
}

/// Human-readable form of the daemon's management-API posture (never reveals
/// a configured token, only whether one is required).
fn describe_admin_auth(auth: &gitstate_daemon::AdminAuth) -> &'static str {
    match auth {
        gitstate_daemon::AdminAuth::LocalOnly => "local-only (no gate; loopback bind only)",
        gitstate_daemon::AdminAuth::Token(_) => "bearer token required (GITSTATE_ADMIN_TOKEN)",
        gitstate_daemon::AdminAuth::DelegatedExternally => {
            "delegated externally (operator opted out of an in-process gate)"
        }
    }
}
