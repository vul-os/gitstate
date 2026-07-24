//! HTTP route handlers — thin JSON adapters over [`crate::ops`]. Each submodule
//! contributes a `Router<AppState>`; [`crate::router`] merges them, adds the
//! static fallback + CORS, and installs the shared state.

mod categories;
mod classify;
mod contexts;
mod health_metrics;
mod meta;
mod repos;
mod trackers;

use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Serialize;

use gitstate_core::Error;

pub use categories::category_routes;
pub use classify::classify_routes;
pub use contexts::context_routes;
pub use health_metrics::health_routes;
pub use meta::meta_routes;
pub use repos::repo_routes;
pub use trackers::tracker_routes;

/// A JSON error body `{ "error": "...", "code": "snake_code" }` with a status.
#[derive(Debug)]
pub struct ApiError {
    status: StatusCode,
    code: &'static str,
    message: String,
}

#[derive(Serialize)]
struct ApiErrorBody {
    error: String,
    code: &'static str,
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(ApiErrorBody {
                error: self.message,
                code: self.code,
            }),
        )
            .into_response()
    }
}

impl From<Error> for ApiError {
    fn from(e: Error) -> Self {
        let (status, code) = match &e {
            Error::NotFound { .. } => (StatusCode::NOT_FOUND, "not_found"),
            Error::Invalid(_) => (StatusCode::BAD_REQUEST, "invalid"),
            Error::ForgeCliMissing(_) => (StatusCode::BAD_REQUEST, "forge_cli_missing"),
            Error::TaxonomyUntrusted(_) => (StatusCode::BAD_REQUEST, "taxonomy_untrusted"),
            Error::SyncDisabled => (StatusCode::NOT_FOUND, "sync_disabled"),
            Error::Forge(_) => (StatusCode::BAD_GATEWAY, "forge_error"),
            Error::Git(_) => (StatusCode::INTERNAL_SERVER_ERROR, "git_error"),
            Error::Classify(_) => (StatusCode::INTERNAL_SERVER_ERROR, "classify_error"),
            Error::Storage(_) => (StatusCode::INTERNAL_SERVER_ERROR, "storage_error"),
            Error::Http(_) => (StatusCode::INTERNAL_SERVER_ERROR, "http_error"),
            Error::Io(_) => (StatusCode::INTERNAL_SERVER_ERROR, "io_error"),
            Error::Json(_) => (StatusCode::BAD_REQUEST, "json_error"),
        };
        ApiError {
            status,
            code,
            message: e.to_string(),
        }
    }
}

/// Result alias for handlers.
pub type ApiResult<T> = std::result::Result<T, ApiError>;
