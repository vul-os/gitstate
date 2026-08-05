//! `gitstate agent log-run | runs | whoami | context | pr` — the agent-native
//! surface Go's `gittrack` shipped as a standalone binary, now folded into
//! `gitstate-cli` (T11 port plan). `log-run`/`runs`/`whoami` landed in wave 1
//! (`store/agent_runs.go`); `context`/`pr` land here in wave 3
//! (`store/context_bundle.go`) — `gittrack issues` is not ported (it lists
//! plain work items, a different, already-covered surface, not this domain).
//!
//! Like every other `gitstate` subcommand, these call `gitstate_daemon::ops`
//! directly against the local SQLite file — no HTTP, no token. `gittrack` was
//! a separate binary because it spoke to a *remote*, multi-tenant Postgres
//! server over `GITSTATE_URL`/`GITSTATE_TOKEN`; there is no separate server
//! to reach here, so there is nothing left for a bearer token to gate. See
//! `whoami` below for what replaces token validation in a single-user app.
//!
//! `context`/`pr` deliberately sit under `agent`, not as new top-level
//! commands: `gitstate context` already names the CRDT saved-working-set
//! feature (T8) — same word, unrelated feature — so `gitstate agent context
//! <issue-id>` avoids the collision while keeping Go's own wording.

use clap::Subcommand;

use gitstate_core::{AgentDiffSummary, EstimateBrief, IssueContextBundle, PrContextBundle};
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
    /// Fetch the curated context bundle for an issue: the issue itself,
    /// related PRs, recent commits, historically-touched code areas, a
    /// calibrated effort estimate, and similar past issues — trimmed to fit
    /// an agent's context window (`store/context_bundle.go`'s
    /// `BuildIssueContext`, ported T11 wave 3).
    Context {
        /// The issue's id (see `gitstate repo <id> ...` output, or a forge sync).
        issue_id: String,
    },
    /// Fetch the curated context bundle for a pull request: diff shape,
    /// cycle time, and a calibrated effort estimate (`store/context_bundle.go`'s
    /// `BuildPRContext`, ported T11 wave 3).
    Pr {
        /// The PR's id.
        id: String,
    },
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
                println!(
                    "{:<38} {:<20} {:<12} {:<9} goal",
                    "id", "agent", "action", "tests"
                );
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
        AgentCmd::Context { issue_id } => {
            let bundle = ops::build_issue_context(&state, &issue_id.into())?;
            if ctx.json {
                ctx.print_json(&bundle)?;
            } else {
                print_issue_context(&bundle);
            }
        }
        AgentCmd::Pr { id } => {
            let bundle = ops::build_pr_context(&state, &id.into())?;
            if ctx.json {
                ctx.print_json(&bundle)?;
            } else {
                print_pr_context(&bundle);
            }
        }
    }
    Ok(())
}

/// A stable "#N" label, falling back to the bare id for a native item with no
/// platform number. Mirrors `gittrack`'s `issueRef`.
fn issue_ref(number: Option<i64>, id: &str) -> String {
    match number {
        Some(n) if n > 0 => format!("#{n}"),
        _ => id.to_string(),
    }
}

/// Renders a seconds count as a compact human duration (e.g. "2d 3h").
/// Mirrors `gittrack`'s `fmtDuration`; zero/negative renders as "—" (usually
/// "not yet measurable", e.g. an unmerged PR has no lead time).
fn fmt_duration(secs: i64) -> String {
    if secs <= 0 {
        return "—".to_string();
    }
    let days = secs / 86400;
    let rem = secs % 86400;
    let hours = rem / 3600;
    let mins = (rem % 3600) / 60;

    let mut parts = Vec::new();
    if days > 0 {
        parts.push(format!("{days}d"));
    }
    if hours > 0 {
        parts.push(format!("{hours}h"));
    }
    if mins > 0 && days == 0 {
        parts.push(format!("{mins}m"));
    }
    if parts.is_empty() {
        format!("{secs}s")
    } else {
        parts.join(" ")
    }
}

fn print_estimate(est: &EstimateBrief) {
    print!("estimate: difficulty {:.1}", est.difficulty);
    if let Some(secs) = est.predicted_secs {
        print!("  ~{}", fmt_duration(secs.round() as i64));
    }
    match (&est.size_bucket, &est.change_type) {
        (Some(sb), Some(ct)) if !sb.is_empty() || !ct.is_empty() => print!("  ({sb} {ct})"),
        _ => {}
    }
    println!();
}

/// Renders the human-readable issue bundle summary. Mirrors `gittrack`'s
/// `renderIssueContext`.
fn print_issue_context(b: &IssueContextBundle) {
    let iss = &b.issue;
    println!("{}  {}", issue_ref(iss.number, &iss.id.0), iss.title);

    print!("state: {}", iss.state);
    if !iss.labels.is_empty() {
        print!("   labels: {}", iss.labels.join(", "));
    }
    println!();

    if !iss.body.trim().is_empty() {
        println!("\n{}", iss.body.trim());
    }

    if let Some(est) = &b.estimate {
        println!();
        print_estimate(est);
    }

    if !b.related_prs.is_empty() {
        println!("\nRelated PRs ({}):", b.related_prs.len());
        for pr in &b.related_prs {
            let merged = if pr.merged {
                format!(
                    " merged in {}",
                    pr.lead_time_secs
                        .map(fmt_duration)
                        .unwrap_or_else(|| "—".into())
                )
            } else {
                String::new()
            };
            println!(
                "  {} [{}]{}  {}",
                issue_ref(pr.number, &pr.id.0),
                pr.state,
                merged,
                pr.title
            );
        }
    }

    if !b.recent_commits.is_empty() {
        println!("\nRecent commits ({}):", b.recent_commits.len());
        for c in &b.recent_commits {
            let agent = if c.is_agent { "  [agent]" } else { "" };
            println!("  {:<10} {}{}", c.sha, c.subject, agent);
        }
    }

    if !b.code_areas.is_empty() {
        println!("\nHistorically-touched areas ({}):", b.code_areas.len());
        for a in &b.code_areas {
            println!("  {a}");
        }
    }

    if !b.similar_issues.is_empty() {
        println!("\nSimilar past issues ({}):", b.similar_issues.len());
        for s in &b.similar_issues {
            println!(
                "  {} [{}] {}",
                issue_ref(s.number, &s.id.0),
                s.state,
                s.title
            );
            if let Some(pr) = &s.resolved_by_pr {
                println!(
                    "      resolved by {} [{}]",
                    issue_ref(pr.number, &pr.id.0),
                    pr.state
                );
            }
        }
    }
}

/// Renders the human-readable PR bundle summary. Mirrors `gittrack`'s
/// `renderPRContext`.
fn print_pr_context(b: &PrContextBundle) {
    let pr = &b.pr;
    println!("{}  {}", issue_ref(pr.number, &pr.id.0), pr.title);

    let state = if pr.merged {
        format!("{} (merged)", pr.state)
    } else {
        pr.state.clone()
    };
    print!("state: {state}");
    if let Some(a) = &pr.author_login {
        print!("   author: {a}");
    }
    let d = &b.diff_summary;
    println!(
        "   +{}/-{} across {} files",
        d.additions, d.deletions, d.changed_files
    );

    match b.cycle_time_secs {
        Some(secs) => print!("cycle time: {}", fmt_duration(secs)),
        None => print!("cycle time: —"),
    }
    if let Some(est) = &b.estimate {
        if let Some(secs) = est.predicted_secs {
            print!("   estimated effort: {}", fmt_duration(secs.round() as i64));
            if let Some(sb) = &est.size_bucket {
                if !sb.is_empty() {
                    print!(" ({sb})");
                }
            }
        }
    }
    println!();
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
