//! `gitstate state | contributions | contributors | classify | effort`.

use clap::Args;

use gitstate_core::Weights;
use gitstate_daemon::ops;

use super::Ctx;

#[derive(Debug, Args)]
pub struct ContribArgs {
    pub repo_id: String,
    /// Window start (RFC3339). Default: all-time.
    #[arg(long)]
    pub from: Option<String>,
    /// Window end (RFC3339). Default: all-time.
    #[arg(long)]
    pub to: Option<String>,
    /// Override composite weights, e.g. `shipped=2,review=1,quality=3`.
    #[arg(long)]
    pub weights: Option<String>,
}

#[derive(Debug, Args)]
pub struct ClassifyArgs {
    pub repo_id: String,
    /// Comma-separated work-item refs (e.g. `#12,#40`). Default: all applicable.
    #[arg(long, value_delimiter = ',')]
    pub items: Option<Vec<String>>,
}

pub fn state(ctx: &Ctx, repo_id: &str) -> anyhow::Result<()> {
    let state = ctx.state()?;
    let repo = super::resolve_repo(&state, repo_id)?;
    let ps = ops::project_state(&state, &repo)?;
    if ctx.json {
        ctx.print_json(&ps)?;
    } else {
        println!("repo         {}", ps.repo_id);
        println!("head         {}", ps.head_sha);
        println!(
            "prs          open={} merged={} draft={}",
            ps.open_prs, ps.merged_prs, ps.draft_prs
        );
        println!(
            "issues       open={} closed={}",
            ps.open_issues, ps.closed_issues
        );
        println!(
            "flow         in_progress={} done={}",
            ps.in_progress, ps.done
        );
        println!(
            "cycle time   p50={} p90={} (hours)",
            fmt_opt(ps.cycle_time_p50_hours),
            fmt_opt(ps.cycle_time_p90_hours)
        );
        println!("change fail  {}", fmt_opt(ps.change_failure_rate));
        for w in &ps.warnings {
            println!("warning:     {w}");
        }
    }
    Ok(())
}

pub fn contributions(ctx: &Ctx, args: ContribArgs) -> anyhow::Result<()> {
    let state = ctx.state()?;
    // Persist an explicit weights override before reading, so a re-scan and the
    // stored composite agree; the query itself reads the persisted rows.
    if let Some(spec) = &args.weights {
        let w = parse_weights(spec)?;
        ops::save_weights(state.store.as_ref(), &w)?;
    }
    let repo = super::resolve_repo(&state, &args.repo_id)?;
    let rows = ops::contributions(&state, &repo, args.from.as_deref(), args.to.as_deref())?;
    if ctx.json {
        ctx.print_json(&rows)?;
    } else if rows.is_empty() {
        println!("no contributions (scan the repo first: gitstate repo scan <id>)");
    } else {
        // A raw ContributorId is unreadable in a terminal; resolve it to the
        // merged identity's display name, falling back to the id when the
        // contributor row is missing.
        let names: std::collections::HashMap<String, String> = ops::contributors(&state)?
            .into_iter()
            .map(|c| {
                let name = if c.is_agent {
                    format!("{} [agent]", c.display_name)
                } else {
                    c.display_name
                };
                (c.id.to_string(), name)
            })
            .collect();
        println!(
            "{:<30} {:>6} {:>6} {:>6} {:>6} {:>6} {:>6} {:>7} {:>6}",
            "contributor", "ship", "rev", "eff", "qual", "own", "dur", "comp", "agent%"
        );
        for c in &rows {
            let d = &c.dimensions;
            let who = c.contributor_id.to_string();
            println!(
                "{:<30} {:>6.0} {:>6.0} {:>6.0} {:>6.0} {:>6.0} {:>6.0} {:>7.1} {:>5.0}%",
                names.get(&who).unwrap_or(&who),
                d.shipped,
                d.review,
                d.effort,
                d.quality,
                d.ownership,
                d.durability,
                c.composite,
                c.agent_pct * 100.0
            );
        }
    }
    Ok(())
}

pub fn contributors(ctx: &Ctx) -> anyhow::Result<()> {
    let state = ctx.state()?;
    let rows = ops::contributors(&state)?;
    if ctx.json {
        ctx.print_json(&rows)?;
    } else if rows.is_empty() {
        println!("no contributors yet");
    } else {
        for c in &rows {
            let agent = if c.is_agent {
                format!(" [agent:{}]", c.agent_kind.as_deref().unwrap_or("?"))
            } else {
                String::new()
            };
            println!(
                "{}  {:<24} {}{}",
                c.id, c.display_name, c.primary_email, agent
            );
        }
    }
    Ok(())
}

pub async fn classify(ctx: &Ctx, args: ClassifyArgs) -> anyhow::Result<()> {
    let state = ctx.state()?;
    let repo = super::resolve_repo(&state, &args.repo_id)?;
    let out = ops::classify_items(&state, &repo, args.items).await?;
    if ctx.json {
        ctx.print_json(&out)?;
    } else if out.is_empty() {
        println!("nothing to classify");
    } else {
        for c in &out {
            println!(
                "{}  {:<16} {:.2}  [{}]",
                c.item_id,
                c.category_key,
                c.confidence,
                c.method.as_str()
            );
        }
    }
    Ok(())
}

pub async fn effort(ctx: &Ctx, args: ClassifyArgs) -> anyhow::Result<()> {
    let state = ctx.state()?;
    let repo = super::resolve_repo(&state, &args.repo_id)?;
    let out = ops::effort_items(&state, &repo, args.items).await?;
    if ctx.json {
        ctx.print_json(&out)?;
    } else if out.is_empty() {
        println!("nothing to judge");
    } else {
        for e in &out {
            println!(
                "{}  difficulty={:.1}  conf={:.2}  [{}]",
                e.item_id,
                e.difficulty,
                e.confidence,
                e.method.as_str()
            );
        }
    }
    Ok(())
}

fn fmt_opt(v: Option<f64>) -> String {
    v.map(|x| format!("{x:.1}"))
        .unwrap_or_else(|| "-".to_string())
}

fn parse_weights(spec: &str) -> anyhow::Result<Weights> {
    let mut w = Weights::default_weights();
    for pair in spec.split(',').filter(|s| !s.is_empty()) {
        let (k, v) = pair
            .split_once('=')
            .ok_or_else(|| anyhow::anyhow!("bad weight (want k=v): {pair}"))?;
        let val: f64 = v.parse()?;
        match k.trim() {
            "shipped" => w.shipped = val,
            "review" => w.review = val,
            "effort" => w.effort = val,
            "quality" => w.quality = val,
            "ownership" => w.ownership = val,
            "durability" => w.durability = val,
            other => anyhow::bail!("unknown dimension: {other}"),
        }
    }
    Ok(w)
}
