//! Cross-cutting routes: `/health`, `/api/contributors`, `/api/taxonomy*`,
//! `/api/sync/*`.

use axum::extract::State;
use axum::routing::{get, post};
use axum::{Json, Router};

use gitstate_core::{Contributor, SyncStatus, Taxonomy};

use super::{ApiError, ApiResult};
use crate::dto::{HealthResp, PublishResp, SyncPublishReq, VerifyResp};
use crate::ops;
use crate::state::AppState;

pub fn meta_routes() -> Router<AppState> {
    Router::new()
        .route("/health", get(health))
        .route("/api/contributors", get(contributors))
        .route("/api/taxonomy", get(taxonomy))
        .route("/api/taxonomy/verify", post(taxonomy_verify))
        .route("/api/sync/status", get(sync_status))
        .route("/api/sync/publish", post(sync_publish))
}

async fn health(State(state): State<AppState>) -> Json<HealthResp> {
    Json(HealthResp {
        status: "ok",
        version: env!("CARGO_PKG_VERSION"),
        sync: state.sync.is_some(),
        classifier: state.classifier.capability().as_str(),
    })
}

async fn contributors(State(state): State<AppState>) -> ApiResult<Json<Vec<Contributor>>> {
    Ok(Json(ops::contributors(&state)?))
}

async fn taxonomy(State(state): State<AppState>) -> Json<Taxonomy> {
    Json(ops::taxonomy(&state))
}

async fn taxonomy_verify(
    State(_state): State<AppState>,
    Json(tax): Json<Taxonomy>,
) -> ApiResult<Json<VerifyResp>> {
    let id = ops::verify_taxonomy(&tax).map_err(ApiError::from)?;
    Ok(Json(VerifyResp { valid: true, id }))
}

async fn sync_status(State(state): State<AppState>) -> ApiResult<Json<SyncStatus>> {
    Ok(Json(ops::sync_status(&state).await?))
}

async fn sync_publish(
    State(state): State<AppState>,
    body: Option<Json<SyncPublishReq>>,
) -> ApiResult<Json<PublishResp>> {
    let since = body.and_then(|Json(b)| b.since);
    let published = ops::sync_publish(&state, since).await?;
    Ok(Json(PublishResp { published }))
}
