//! `/api/contexts*` routes — the sharable saved working sets (CRDT-backed).

use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::routing::get;
use axum::{Json, Router};

use gitstate_core::{Context, ContextId};

use super::ApiResult;
use crate::dto::{ContextPatch, DeletedResp, NewContext};
use crate::ops;
use crate::state::AppState;

pub fn context_routes() -> Router<AppState> {
    Router::new()
        .route("/api/contexts", get(list).post(create))
        .route(
            "/api/contexts/{id}",
            get(get_one).patch(patch).delete(delete_one),
        )
}

async fn list(State(state): State<AppState>) -> ApiResult<Json<Vec<Context>>> {
    Ok(Json(ops::list_contexts(&state)?))
}

async fn get_one(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<Context>> {
    Ok(Json(ops::get_context(&state, &ContextId::from(id))?))
}

async fn create(
    State(state): State<AppState>,
    Json(req): Json<NewContext>,
) -> ApiResult<(StatusCode, Json<Context>)> {
    Ok((StatusCode::CREATED, Json(ops::create_context(&state, req)?)))
}

async fn patch(
    State(state): State<AppState>,
    Path(id): Path<String>,
    Json(body): Json<ContextPatch>,
) -> ApiResult<Json<Context>> {
    Ok(Json(ops::patch_context(
        &state,
        &ContextId::from(id),
        body,
    )?))
}

async fn delete_one(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<DeletedResp>> {
    ops::delete_context(&state, &ContextId::from(id))?;
    Ok(Json(DeletedResp { deleted: true }))
}
