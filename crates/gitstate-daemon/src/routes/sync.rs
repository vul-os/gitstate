//! The peer endpoints — the only routes a gitstate node exposes to another node,
//! and the only ones that authenticate.
//!
//! * `GET  /api/sync/pull` — hand the caller every signed op after its cursor.
//! * `POST /api/sync/push` — take signed ops from the caller.
//! * `GET  /api/sync/identity` — this node's peer id and public key, for an
//!   operator enrolling it on the other side. **Local-only**, see below.
//! * `GET|POST|DELETE /api/sync/peers` — manage enrolments. Local-only.
//!
//! # Two different audiences, two different rules
//!
//! `pull` and `push` are spoken by *peers*. They require a signed request token
//! (`super::super::` → `gitstate_sync::auth`), they are refused outright when the
//! caller is not an enrolled peer, and every response body is signed with this
//! node's key so the caller can confirm which node answered.
//!
//! `identity` and `peers` are spoken by the *operator* — they enrol peers and
//! reveal this node's key. They are not peer endpoints and must not be reachable
//! from the internet on an exposed node, so they sit behind the same
//! local-administration guard as the rest of the management API
//! (`crate::admin_guard`).
//!
//! # Why the response is signed rather than just TLS-protected
//!
//! An operator may legitimately run a node over `http://` on a private link. The
//! response signature means the caller's trust is anchored on the key it enrolled
//! out of band, not on whatever answered at that address — so a wrong DNS answer
//! or a transparent proxy produces a refusal instead of accepted ops.

use axum::body::Bytes;
use axum::extract::{Path, Query, State};
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use serde::Deserialize;

use gitstate_core::{
    normalize_peer_url, normalize_pubkey, now_rfc3339, Error, Hlc, PeerId, SyncPeer, SyncPullResp,
    SyncPushReq,
};
use gitstate_sync::auth::{authenticate, sign_response, SIG_HEADER};
use gitstate_sync::{ingest_signed, PULL_PATH, PUSH_PATH};

use super::{ApiError, ApiResult};
use crate::dto::{NodeIdentityResp, PeerEnrolReq, SyncRunResp};
use crate::ops;
use crate::state::AppState;

/// The peer-facing half: authenticated by key, not by the operator's admin
/// token. Merged OUTSIDE the management gate on purpose (see `crate::router`).
pub fn sync_peer_routes() -> Router<AppState> {
    Router::new()
        .route(PULL_PATH, get(pull))
        .route(PUSH_PATH, post(push))
}

/// The operator-facing half: enrolment, this node's key, and "sync now". Merged
/// INSIDE the management gate.
pub fn sync_admin_routes() -> Router<AppState> {
    Router::new()
        .route("/api/sync/identity", get(identity))
        .route("/api/sync/peers", get(list_peers).post(add_peer))
        .route("/api/sync/peers/{id}", delete(remove_peer))
        .route("/api/sync/run", post(run))
}

#[derive(Debug, Deserialize)]
struct SinceQuery {
    since: Option<String>,
}

/// A body plus this node's signature over exactly those bytes.
struct Signed(Vec<u8>, String);

impl IntoResponse for Signed {
    fn into_response(self) -> Response {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        if let Ok(v) = HeaderValue::from_str(&self.1) {
            headers.insert(SIG_HEADER, v);
        }
        (StatusCode::OK, headers, self.0).into_response()
    }
}

/// Serialize `value` and sign the exact bytes returned.
fn signed_json<T: serde::Serialize>(state: &AppState, value: &T) -> ApiResult<Signed> {
    let body = serde_json::to_vec(value).map_err(|e| ApiError::from(Error::from(e)))?;
    let identity = gitstate_sync::node_identity(state.store.as_ref()).map_err(ApiError::from)?;
    let sig = sign_response(&identity, &body);
    Ok(Signed(body, sig))
}

async fn pull(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(q): Query<SinceQuery>,
) -> ApiResult<Signed> {
    authenticate(
        state.store.as_ref(),
        &state.replay_guard,
        headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok()),
        "GET",
        PULL_PATH,
    )
    .map_err(ApiError::from)?;

    // An unparseable cursor is treated as no cursor: the caller then receives
    // everything and re-converges. Silently narrowing the answer on a bad cursor
    // would be the lost-write direction.
    let since = q.since.as_deref().and_then(|s| Hlc::decode(s).ok());
    let ops_out = state
        .store
        .signed_ops_since(since.as_ref())
        .map_err(ApiError::from)?;
    let peer_id = PeerId::from(
        state
            .store
            .kv_get("peer_id")
            .ok()
            .flatten()
            .unwrap_or_default(),
    );
    signed_json(
        &state,
        &SyncPullResp {
            peer_id,
            ops: ops_out,
        },
    )
}

async fn push(State(state): State<AppState>, headers: HeaderMap, body: Bytes) -> ApiResult<Signed> {
    authenticate(
        state.store.as_ref(),
        &state.replay_guard,
        headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok()),
        "POST",
        PUSH_PATH,
    )
    .map_err(ApiError::from)?;

    let req: SyncPushReq =
        serde_json::from_slice(&body).map_err(|e| ApiError::from(Error::from(e)))?;
    // The connection authenticated, and every op is STILL verified on its own —
    // including against a different enrolled peer than the one that dialled us,
    // because a relayed op's author is not the caller.
    let outcome = ingest_signed(state.store.as_ref(), &req.ops).map_err(ApiError::from)?;
    signed_json(&state, &outcome)
}

// ─────────────────── operator-facing (local only) ───────────────────

async fn identity(State(state): State<AppState>) -> ApiResult<Json<NodeIdentityResp>> {
    Ok(Json(ops::node_identity_view(&state)?))
}

async fn list_peers(State(state): State<AppState>) -> ApiResult<Json<Vec<SyncPeer>>> {
    Ok(Json(state.store.list_sync_peers()?))
}

async fn add_peer(
    State(state): State<AppState>,
    Json(req): Json<PeerEnrolReq>,
) -> ApiResult<Json<SyncPeer>> {
    let peer = SyncPeer {
        id: PeerId::from(req.id.trim()),
        url: normalize_peer_url(&req.url)?,
        pubkey: normalize_pubkey(&req.pubkey)?,
        label: req.label,
        added_at: now_rfc3339(),
        last_pull_hlc: None,
    };
    if peer.id.0.is_empty() {
        return Err(ApiError::from(Error::invalid("peer id is required")));
    }
    state.store.upsert_sync_peer(&peer)?;
    Ok(Json(peer))
}

async fn remove_peer(
    State(state): State<AppState>,
    Path(id): Path<String>,
) -> ApiResult<Json<serde_json::Value>> {
    let removed = state.store.delete_sync_peer(&PeerId::from(id.as_str()))?;
    Ok(Json(serde_json::json!({ "removed": removed })))
}

async fn run(State(state): State<AppState>) -> ApiResult<Json<SyncRunResp>> {
    Ok(Json(ops::sync_run(&state).await?))
}
