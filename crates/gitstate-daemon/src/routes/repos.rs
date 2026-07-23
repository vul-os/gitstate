//! `/api/repos*` routes: register, list, delete, scan, and the derived reads
//! (project-state, contributions, work-items).

use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::Deserialize;

use gitstate_core::{Contribution, ProjectState, Repo, RepoId, WorkItem};

use super::ApiResult;
use crate::dto::{AddRepoReq, DeletedResp, ScanReq, ScanResult};
use crate::ops;
use crate::state::AppState;

pub fn repo_routes() -> Router<AppState> {
    Router::new()
        .route("/api/repos", get(list).post(add))
        .route("/api/repos/{id}", axum::routing::delete(delete_one))
        .route("/api/repos/{id}/scan", post(scan))
        .route("/api/repos/{id}/project-state", get(project_state))
        .route("/api/repos/{id}/contributions", get(contributions))
        .route("/api/repos/{id}/work-items", get(work_items))
        .route("/api/analytics", get(analytics))
}

#[derive(Debug, Deserialize)]
struct AnalyticsQuery {
    repo_id: Option<String>,
    /// Trailing window size in days (default 180). Ignored when `from` is given.
    days: Option<u32>,
    from: Option<String>,
    to: Option<String>,
}

/// `GET /api/analytics` — the one round-trip that feeds the dashboard and the
/// insights screen: heatmap, trends, leaderboard and headline totals.
async fn analytics(
    State(state): State<AppState>,
    Query(q): Query<AnalyticsQuery>,
) -> ApiResult<Json<gitstate_core::Analytics>> {
    let repo = q.repo_id.filter(|s| !s.is_empty()).map(RepoId::from);
    Ok(Json(ops::analytics(
        &state,
        repo.as_ref(),
        q.days,
        q.from.as_deref(),
        q.to.as_deref(),
    )?))
}

async fn list(State(state): State<AppState>) -> ApiResult<Json<Vec<Repo>>> {
    Ok(Json(ops::list_repos(&state)?))
}

async fn add(
    State(state): State<AppState>,
    Json(req): Json<AddRepoReq>,
) -> ApiResult<(StatusCode, Json<Repo>)> {
    let repo = ops::add_repo(&state, req.path, req.remote_url)?;
    Ok((StatusCode::CREATED, Json(repo)))
}

async fn delete_one(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<DeletedResp>> {
    ops::delete_repo(&state, &RepoId::from(id))?;
    Ok(Json(DeletedResp { deleted: true }))
}

async fn scan(
    State(state): State<AppState>,
    Path(id): Path<String>,
    body: Option<Json<ScanReq>>,
) -> ApiResult<Json<ScanResult>> {
    let req = body.map(|Json(b)| b).unwrap_or_default();
    let res = ops::scan_repo(&state, &RepoId::from(id), req.with_forge, req.since).await?;
    Ok(Json(res))
}

async fn project_state(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<ProjectState>> {
    Ok(Json(ops::project_state(&state, &RepoId::from(id))?))
}

#[derive(Debug, Deserialize)]
struct ContribQuery {
    from: Option<String>,
    to: Option<String>,
}

async fn contributions(
    State(state): State<AppState>,
    Path(id): Path<String>,
    Query(q): Query<ContribQuery>,
) -> ApiResult<Json<Vec<Contribution>>> {
    let rows = ops::contributions(
        &state,
        &RepoId::from(id),
        q.from.as_deref(),
        q.to.as_deref(),
    )?;
    Ok(Json(rows))
}

#[derive(Debug, Deserialize)]
struct WorkItemQuery {
    kind: Option<String>,
    state: Option<String>,
}

async fn work_items(
    State(state): State<AppState>,
    Path(id): Path<String>,
    Query(q): Query<WorkItemQuery>,
) -> ApiResult<Json<Vec<WorkItem>>> {
    let rows = ops::work_items(
        &state,
        &RepoId::from(id),
        q.kind.as_deref(),
        q.state.as_deref(),
    )?;
    Ok(Json(rows))
}
