//! `/api/categories*` routes — the CRDT-backed label set (taxonomy + local).

use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::routing::get;
use axum::{Json, Router};

use gitstate_core::Category;

use super::ApiResult;
use crate::dto::{CategoryPatch, DeletedResp, NewCategory};
use crate::ops;
use crate::state::AppState;

pub fn category_routes() -> Router<AppState> {
    Router::new()
        .route("/api/categories", get(list).post(create))
        .route(
            "/api/categories/{id}",
            axum::routing::patch(patch).delete(delete_one),
        )
}

async fn list(State(state): State<AppState>) -> ApiResult<Json<Vec<Category>>> {
    Ok(Json(ops::list_categories(&state)?))
}

async fn create(
    State(state): State<AppState>,
    Json(req): Json<NewCategory>,
) -> ApiResult<(StatusCode, Json<Category>)> {
    Ok((
        StatusCode::CREATED,
        Json(ops::create_category(&state, req)?),
    ))
}

async fn patch(
    State(state): State<AppState>,
    Path(id): Path<String>,
    Json(body): Json<CategoryPatch>,
) -> ApiResult<Json<Category>> {
    let cat = ops::patch_category(&state, &id, body.label, body.color, body.parent_key)?;
    Ok(Json(cat))
}

async fn delete_one(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<DeletedResp>> {
    ops::delete_category(&state, &id)?;
    Ok(Json(DeletedResp { deleted: true }))
}
