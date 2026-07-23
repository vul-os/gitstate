//! Static serving of the built React app (`web/dist`) with SPA fallback.
//!
//! Any path that reached here is not an `/api` route. If it maps to a real file
//! under `web_dist`, serve it with a guessed content-type; otherwise serve
//! `index.html` so client-side routing (BrowserRouter) works on deep links —
//! the ofisi/slipscan-server pattern. Path traversal is refused. If no
//! `web_dist` is configured (API-only mode), return 404.

use std::path::{Component, Path, PathBuf};

use axum::body::Body;
use axum::extract::State;
use axum::http::{header, StatusCode, Uri};
use axum::response::{IntoResponse, Response};

use crate::state::AppState;

pub async fn static_handler(State(state): State<AppState>, uri: Uri) -> Response {
    let Some(dist) = state.web_dist.clone() else {
        return (StatusCode::NOT_FOUND, "not found").into_response();
    };

    let rel = uri.path().trim_start_matches('/');
    if let Some(resolved) = safe_join(&dist, rel) {
        if resolved.is_file() {
            return serve_file(&resolved).await;
        }
    }
    // SPA fallback.
    serve_file(&dist.join("index.html")).await
}

/// Join `rel` onto `base`, rejecting any traversal (`..`, absolute, prefix).
fn safe_join(base: &Path, rel: &str) -> Option<PathBuf> {
    if rel.is_empty() {
        return None;
    }
    let candidate = Path::new(rel);
    for comp in candidate.components() {
        match comp {
            Component::Normal(_) => {}
            _ => return None,
        }
    }
    Some(base.join(candidate))
}

async fn serve_file(path: &Path) -> Response {
    match std::fs::read(path) {
        Ok(bytes) => {
            let ctype = content_type(path);
            ([(header::CONTENT_TYPE, ctype)], Body::from(bytes)).into_response()
        }
        Err(_) => (StatusCode::NOT_FOUND, "not found").into_response(),
    }
}

fn content_type(path: &Path) -> &'static str {
    let ext = path
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    match ext.as_str() {
        "html" | "htm" => "text/html; charset=utf-8",
        "js" | "mjs" => "text/javascript; charset=utf-8",
        "css" => "text/css; charset=utf-8",
        "json" => "application/json",
        "svg" => "image/svg+xml",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "webp" => "image/webp",
        "ico" => "image/x-icon",
        "woff" => "font/woff",
        "woff2" => "font/woff2",
        "ttf" => "font/ttf",
        "map" => "application/json",
        "txt" => "text/plain; charset=utf-8",
        "wasm" => "application/wasm",
        _ => "application/octet-stream",
    }
}
