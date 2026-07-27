//! `/api/classify*` and `/api/effort` routes — local classification + effort
//! judging over a repo's work items.

use axum::extract::{Path, State};
use axum::routing::{get, post};
use axum::{Json, Router};

use gitstate_core::{Classification, EffortEstimate, RepoId, WorkItemId};

use super::ApiResult;
use crate::dto::{ClassifyReq, FeedbackReq, OkResp};
use crate::ops;
use crate::state::AppState;

pub fn classify_routes() -> Router<AppState> {
    Router::new()
        .route("/api/classify", post(classify))
        .route("/api/classify/feedback", post(feedback))
        .route("/api/effort", post(effort))
        // Read-only companions to the two POSTs above: what has already been
        // judged for this repo, so a UI can show stored labels and difficulty
        // without re-running (and re-writing) anything.
        .route("/api/repos/{id}/classifications", get(list_classifications))
        .route("/api/repos/{id}/effort", get(list_effort))
}

async fn list_classifications(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<Vec<Classification>>> {
    Ok(Json(state.store.list_classifications(&RepoId::from(id))?))
}

async fn list_effort(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<Vec<EffortEstimate>>> {
    Ok(Json(state.store.list_effort(&RepoId::from(id))?))
}

async fn classify(
    State(state): State<AppState>,
    Json(req): Json<ClassifyReq>,
) -> ApiResult<Json<Vec<Classification>>> {
    let out = ops::classify_items(&state, &RepoId::from(req.repo_id), req.item_ids).await?;
    Ok(Json(out))
}

async fn feedback(
    State(state): State<AppState>,
    Json(req): Json<FeedbackReq>,
) -> ApiResult<Json<OkResp>> {
    ops::record_feedback(&state, &WorkItemId::from(req.item_id), &req.category_key)?;
    Ok(Json(OkResp { ok: true }))
}

async fn effort(
    State(state): State<AppState>,
    Json(req): Json<ClassifyReq>,
) -> ApiResult<Json<Vec<EffortEstimate>>> {
    let out = ops::effort_items(&state, &RepoId::from(req.repo_id), req.item_ids).await?;
    Ok(Json(out))
}
