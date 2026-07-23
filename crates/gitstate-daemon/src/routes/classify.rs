//! `/api/classify*` and `/api/effort` routes — local classification + effort
//! judging over a repo's work items.

use axum::extract::State;
use axum::routing::post;
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
