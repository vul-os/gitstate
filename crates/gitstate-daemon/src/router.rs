//! Composes the full router: the JSON API, the static SPA fallback, and a
//! localhost-only CORS layer. Any non-`/api` (and non-`/health`) path falls
//! through to `web/dist` with `index.html` SPA fallback.

use axum::extract::Request;
use axum::http::{header, HeaderValue, Method, StatusCode};
use axum::middleware::{self, Next};
use axum::response::Response;
use axum::Router;

use crate::routes;
use crate::serve_static;
use crate::state::AppState;

pub fn build_router(state: AppState) -> Router {
    // The management API: everything an operator drives. Gated by
    // `require_admin`, which is a no-op on a loopback bind and a bearer check on
    // an exposed one (see `state::AdminAuth`).
    let admin_api = Router::new()
        .merge(routes::meta_routes())
        .merge(routes::repo_routes())
        .merge(routes::context_routes())
        .merge(routes::category_routes())
        .merge(routes::classify_routes())
        .merge(routes::health_routes())
        .merge(routes::tracker_routes())
        .merge(routes::sync_admin_routes())
        .merge(routes::agent_run_routes())
        .route_layer(middleware::from_fn_with_state(state.clone(), require_admin));

    // The peer API. NOT under `require_admin`: it carries its own, stronger
    // authentication (a per-request signed token from an enrolled peer, plus a
    // signature on every individual op), and a peer has no reason to hold the
    // operator's admin token. Keeping the two gates separate is what lets a
    // cloud node be dialled by peers while its management surface stays shut.
    let peer_api = routes::sync_peer_routes();

    admin_api
        .merge(peer_api)
        .merge(routes::public_routes())
        .fallback(serve_static::static_handler)
        .with_state(state)
        .layer(middleware::from_fn(cors_localhost))
}

/// The management-API gate. Refuses with 401 and no detail when the configured
/// posture is a token and the request does not carry it.
async fn require_admin(
    axum::extract::State(state): axum::extract::State<AppState>,
    req: Request,
    next: Next,
) -> Response {
    let presented = req
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());
    if state.admin_ok(presented) {
        return next.run(req).await;
    }
    let mut resp = Response::new(axum::body::Body::from(
        r#"{"error":"unauthenticated","code":"unauthenticated"}"#,
    ));
    *resp.status_mut() = StatusCode::UNAUTHORIZED;
    resp.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    resp
}

/// Permissive CORS for localhost/loopback and the Tauri webview origin only.
/// The desktop shell's webview runs at a `tauri://` (or `http://tauri.localhost`)
/// origin and calls the ephemeral daemon cross-origin; headless/browser use is
/// same-origin and needs nothing. We never reflect a non-local origin.
async fn cors_localhost(req: Request, next: Next) -> Response {
    let origin = req
        .headers()
        .get(header::ORIGIN)
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());
    let is_preflight = req.method() == Method::OPTIONS;

    let allow = origin
        .as_deref()
        .filter(|o| is_local_origin(o))
        .map(String::from);

    if is_preflight {
        let mut resp = Response::new(axum::body::Body::empty());
        *resp.status_mut() = StatusCode::NO_CONTENT;
        apply_cors(resp.headers_mut(), allow.as_deref());
        return resp;
    }

    let mut resp = next.run(req).await;
    apply_cors(resp.headers_mut(), allow.as_deref());
    resp
}

fn is_local_origin(origin: &str) -> bool {
    let o = origin.to_ascii_lowercase();
    o.starts_with("http://localhost")
        || o.starts_with("http://127.0.0.1")
        || o.starts_with("https://localhost")
        || o.starts_with("https://127.0.0.1")
        || o.starts_with("http://[::1]")
        || o.starts_with("tauri://")
        || o.starts_with("http://tauri.localhost")
        || o.starts_with("https://tauri.localhost")
}

fn apply_cors(headers: &mut header::HeaderMap, allow_origin: Option<&str>) {
    if let Some(origin) = allow_origin {
        if let Ok(v) = HeaderValue::from_str(origin) {
            headers.insert(header::ACCESS_CONTROL_ALLOW_ORIGIN, v);
        }
        headers.insert(
            header::ACCESS_CONTROL_ALLOW_METHODS,
            HeaderValue::from_static("GET, POST, PATCH, DELETE, OPTIONS"),
        );
        headers.insert(
            header::ACCESS_CONTROL_ALLOW_HEADERS,
            HeaderValue::from_static("content-type"),
        );
        headers.insert(header::VARY, HeaderValue::from_static("Origin"));
    }
}
