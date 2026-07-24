//! `/api/health-metrics`, `/api/involvement` and `/api/contributions/rollup`.
//!
//! These read the derived caches and hand them to the pure rollups in
//! [`gitstate_core::health`] — no derivation logic lives here, so every number
//! the Eng Health / Involvement / Contribution screens render is unit-tested
//! in core rather than only exercised through HTTP.

use std::collections::BTreeMap;

use axum::extract::{Query, State};
use axum::routing::get;
use axum::{Json, Router};
use serde::{Deserialize, Serialize};

use gitstate_core::analytics;
use gitstate_core::health;
use gitstate_core::{Contribution, DimensionRaw, Dimensions, RepoId};

use super::ApiResult;
use crate::ops;
use crate::state::AppState;

pub fn health_routes() -> Router<AppState> {
    Router::new()
        .route("/api/health-metrics", get(health_metrics))
        .route("/api/involvement", get(involvement))
        .route("/api/contributions/rollup", get(contributions_rollup))
}

#[derive(Debug, Deserialize)]
struct WindowQuery {
    repo_id: Option<String>,
    days: Option<u32>,
    from: Option<String>,
    to: Option<String>,
}

/// Resolve the query window exactly the way `/api/analytics` does, so the
/// screens agree with each other: anchor on the newest commit (not wall-clock
/// now) unless the caller pinned an explicit bound.
fn resolve_window(
    state: &AppState,
    repo: Option<&RepoId>,
    q: &WindowQuery,
) -> gitstate_core::Result<(String, String)> {
    let commits = state.store.list_commits(repo)?;
    let anchor =
        q.to.as_deref()
            .and_then(|t| analytics::day_key(t).map(str::to_string))
            .or_else(|| {
                commits
                    .iter()
                    .filter_map(|c| analytics::day_key(&c.committed_at))
                    .max()
                    .map(str::to_string)
            })
            .unwrap_or_else(|| gitstate_core::now_rfc3339()[..10].to_string());

    match q
        .from
        .as_deref()
        .and_then(|f| analytics::day_key(f).map(str::to_string))
    {
        Some(f) => Ok((f, anchor)),
        None => {
            let days = q.days.unwrap_or(180).clamp(1, ops::MAX_ANALYTICS_DAYS);
            analytics::range_ending(&anchor, days).ok_or_else(|| {
                gitstate_core::Error::invalid(format!("bad analytics anchor date: {anchor}"))
            })
        }
    }
}

async fn health_metrics(
    State(state): State<AppState>,
    Query(q): Query<WindowQuery>,
) -> ApiResult<Json<health::EngHealth>> {
    let repo = q
        .repo_id
        .clone()
        .filter(|s| !s.is_empty())
        .map(RepoId::from);
    let (from, to) = resolve_window(&state, repo.as_ref(), &q)?;

    let commits = state.store.list_commits(repo.as_ref())?;
    let items = match &repo {
        Some(r) => state.store.list_work_items(r)?,
        None => state.store.list_all_work_items()?,
    };
    let known = state.store.list_contributors()?;

    Ok(Json(health::compute(&commits, &items, &known, &from, &to)))
}

async fn involvement(
    State(state): State<AppState>,
    Query(q): Query<WindowQuery>,
) -> ApiResult<Json<health::Involvement>> {
    let repo = q
        .repo_id
        .clone()
        .filter(|s| !s.is_empty())
        .map(RepoId::from);
    let (from, to) = resolve_window(&state, repo.as_ref(), &q)?;

    let commits = state.store.list_commits(repo.as_ref())?;
    let known = state.store.list_contributors()?;
    let repos: Vec<(String, String)> = state
        .store
        .list_repos()?
        .into_iter()
        .filter(|r| repo.as_ref().is_none_or(|want| &r.id == want))
        .map(|r| (r.id.0, r.slug))
        .collect();

    Ok(Json(health::involvement(
        &commits, &repos, &known, &from, &to,
    )))
}

/// One contributor's six dimensions merged across every repo they touched.
///
/// Per-repo `Contribution` rows are normalized *within* their repo, so they are
/// not comparable across repos on their own. Merging them by a commit-weighted
/// mean keeps the "texture, not a leaderboard" framing intact while giving one
/// row per person.
#[derive(Debug, Clone, Serialize)]
struct RollupRow {
    contributor_id: String,
    display_name: String,
    primary_email: String,
    is_agent: bool,
    dimensions: Dimensions,
    raw: DimensionRaw,
    agent_pct: f64,
    composite: f64,
    repos: Vec<String>,
}

async fn contributions_rollup(
    State(state): State<AppState>,
    Query(q): Query<WindowQuery>,
) -> ApiResult<Json<Vec<RollupRow>>> {
    let from = q.from.as_deref().unwrap_or(ops::WINDOW_FROM);
    let to = q.to.as_deref().unwrap_or(ops::WINDOW_TO);

    let repos = state.store.list_repos()?;
    let known = state.store.list_contributors()?;
    let weights = ops::load_weights(state.store.as_ref())?;

    // contributor_id -> (accumulated rows, repo slugs)
    let mut acc: BTreeMap<String, (Vec<Contribution>, Vec<String>)> = BTreeMap::new();
    for repo in &repos {
        for c in state.store.get_contributions(&repo.id, from, to)? {
            let entry = acc.entry(c.contributor_id.0.clone()).or_default();
            entry.1.push(repo.slug.clone());
            entry.0.push(c);
        }
    }

    let mut out: Vec<RollupRow> = acc
        .into_iter()
        .map(|(id, (rows, mut slugs))| {
            slugs.sort();
            slugs.dedup();
            let merged = health::merge_contributions(&rows, &weights);
            let person = known.iter().find(|k| k.id.0 == id);
            RollupRow {
                display_name: person
                    .map(|p| p.display_name.clone())
                    .unwrap_or_else(|| id.clone()),
                primary_email: person.map(|p| p.primary_email.clone()).unwrap_or_default(),
                is_agent: person.map(|p| p.is_agent).unwrap_or(false),
                contributor_id: id,
                dimensions: merged.dimensions,
                raw: merged.raw,
                agent_pct: merged.agent_pct,
                composite: merged.composite,
                repos: slugs,
            }
        })
        .collect();

    // Highest composite first — presentation order only; the UI is explicit
    // that this is texture, not a ranking.
    out.sort_by(|a, b| {
        b.composite
            .partial_cmp(&a.composite)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then_with(|| a.primary_email.cmp(&b.primary_email))
    });
    Ok(Json(out))
}
