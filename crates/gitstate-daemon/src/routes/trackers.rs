//! `/api/weights`, `/api/trackers*` and `/api/import/*`.
//!
//! **Tracker credentials never leave this machine.** They are stored in the
//! local SQLite `kv` table and used only to call the vendor's own API from
//! here — there is no gitstate server in the path. Reads are always redacted:
//! a token can be written and used, never read back, so a compromised local
//! HTTP client cannot exfiltrate it through this API.

use axum::extract::{Path, State};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};

use gitstate_core::{Error, RepoId, Weights};
use gitstate_tracker::{
    client_for, from_export, to_work_items, ImportedItem, TrackerConfig, TrackerKind, TrackerStatus,
};

use super::{ApiError, ApiResult};
use crate::dto::DeletedResp;
use crate::ops;
use crate::state::AppState;

/// Default and maximum number of issues a single import pulls.
const DEFAULT_LIMIT: usize = 200;
const MAX_LIMIT: usize = 1000;

pub fn tracker_routes() -> Router<AppState> {
    Router::new()
        .route("/api/weights", get(get_weights).put(put_weights))
        .route("/api/weights/reset", post(reset_weights))
        .route("/api/trackers", get(list_trackers))
        .route(
            "/api/trackers/{kind}",
            axum::routing::put(put_tracker).delete(delete_tracker),
        )
        .route("/api/trackers/{kind}/test", post(test_tracker))
        .route("/api/import/preview", post(import_preview))
        .route("/api/import/run", post(import_run))
        .route("/api/import/file", post(import_file))
}

// ─────────────────────────────── weights ───────────────────────────────

async fn get_weights(State(state): State<AppState>) -> ApiResult<Json<Weights>> {
    Ok(Json(ops::load_weights(state.store.as_ref())?))
}

/// Persist new weights. The response is **normalized** (summing to 1) so the
/// UI shows exactly what the composite will actually be computed with.
async fn put_weights(
    State(state): State<AppState>,
    Json(w): Json<Weights>,
) -> ApiResult<Json<Weights>> {
    let sum = w.shipped + w.review + w.effort + w.quality + w.ownership + w.durability;
    if !sum.is_finite() || sum <= 0.0 {
        return Err(ApiError::from(Error::invalid(
            "weights must be finite and sum to more than zero",
        )));
    }
    if [
        w.shipped,
        w.review,
        w.effort,
        w.quality,
        w.ownership,
        w.durability,
    ]
    .iter()
    .any(|v| *v < 0.0)
    {
        return Err(ApiError::from(Error::invalid("weights cannot be negative")));
    }
    let normalized = w.normalized();
    ops::save_weights(state.store.as_ref(), &normalized)?;
    Ok(Json(normalized))
}

async fn reset_weights(State(state): State<AppState>) -> ApiResult<Json<Weights>> {
    let w = Weights::default_weights();
    ops::save_weights(state.store.as_ref(), &w)?;
    Ok(Json(w))
}

// ─────────────────────────────── trackers ───────────────────────────────

fn kv_key(kind: TrackerKind) -> String {
    format!("tracker:{}", kind.as_str())
}

fn load_config(state: &AppState, kind: TrackerKind) -> gitstate_core::Result<TrackerConfig> {
    match state.store.kv_get(&kv_key(kind))? {
        Some(json) => Ok(serde_json::from_str(&json).unwrap_or_default()),
        None => Ok(TrackerConfig::default()),
    }
}

fn store_config(
    state: &AppState,
    kind: TrackerKind,
    cfg: &TrackerConfig,
) -> gitstate_core::Result<()> {
    state
        .store
        .kv_set(&kv_key(kind), &serde_json::to_string(cfg)?)
}

/// A tracker as the UI sees it — `config.token` is the masked hint only.
#[derive(Debug, Serialize)]
pub struct TrackerView {
    pub kind: &'static str,
    pub configured: bool,
    pub base_url: String,
    pub email: String,
    pub project: String,
    /// Masked (`…9f2c`), never the real secret.
    pub token: String,
}

fn view(kind: TrackerKind, cfg: &TrackerConfig) -> TrackerView {
    let r = cfg.redacted();
    TrackerView {
        kind: kind.as_str(),
        configured: cfg.is_configured(),
        base_url: r.base_url,
        email: r.email,
        project: r.project,
        token: r.token,
    }
}

async fn list_trackers(State(state): State<AppState>) -> ApiResult<Json<Vec<TrackerView>>> {
    let mut out = Vec::new();
    for kind in [TrackerKind::Jira, TrackerKind::Linear] {
        out.push(view(kind, &load_config(&state, kind)?));
    }
    Ok(Json(out))
}

async fn put_tracker(
    State(state): State<AppState>,
    Path(kind): Path<String>,
    Json(mut incoming): Json<TrackerConfig>,
) -> ApiResult<Json<TrackerView>> {
    let kind = TrackerKind::parse(&kind)?;
    let existing = load_config(&state, kind)?;

    // An empty token means "leave the stored secret alone" — that is what lets
    // the UI edit the project or email without making the user re-paste it.
    if incoming.token.trim().is_empty() {
        incoming.token = existing.token;
    }
    store_config(&state, kind, &incoming)?;
    Ok(Json(view(kind, &incoming)))
}

async fn delete_tracker(
    State(state): State<AppState>,
    Path(kind): Path<String>,
) -> ApiResult<Json<DeletedResp>> {
    let kind = TrackerKind::parse(&kind)?;
    store_config(&state, kind, &TrackerConfig::default())?;
    Ok(Json(DeletedResp { deleted: true }))
}

async fn test_tracker(
    State(state): State<AppState>,
    Path(kind): Path<String>,
) -> ApiResult<Json<TrackerStatus>> {
    let kind = TrackerKind::parse(&kind)?;
    let cfg = load_config(&state, kind)?;
    let client = client_for(kind, &cfg)?;
    Ok(Json(client.test().await?))
}

// ─────────────────────────────── import ───────────────────────────────

#[derive(Debug, Deserialize)]
pub struct PreviewReq {
    pub kind: String,
    pub limit: Option<usize>,
}

#[derive(Debug, Serialize)]
pub struct PreviewResp {
    pub items: Vec<ImportedItem>,
    pub count: usize,
}

fn clamp_limit(limit: Option<usize>) -> usize {
    limit.unwrap_or(DEFAULT_LIMIT).clamp(1, MAX_LIMIT)
}

/// Fetch without persisting, so nothing is ever written blind.
async fn import_preview(
    State(state): State<AppState>,
    Json(req): Json<PreviewReq>,
) -> ApiResult<Json<PreviewResp>> {
    let kind = TrackerKind::parse(&req.kind)?;
    let cfg = load_config(&state, kind)?;
    let client = client_for(kind, &cfg)?;
    let items = client.fetch(clamp_limit(req.limit)).await?;
    Ok(Json(PreviewResp {
        count: items.len(),
        items,
    }))
}

#[derive(Debug, Deserialize)]
pub struct RunReq {
    pub kind: String,
    pub repo_id: String,
    pub limit: Option<usize>,
}

#[derive(Debug, Serialize)]
pub struct ImportResp {
    pub imported: u32,
    pub repo_id: String,
}

/// Resolve the target repo, 404ing rather than silently writing orphan rows.
fn require_repo(state: &AppState, repo_id: &str) -> gitstate_core::Result<RepoId> {
    let id = RepoId::from(repo_id.to_string());
    match state.store.get_repo(&id)? {
        Some(_) => Ok(id),
        None => Err(Error::not_found("repo", repo_id.to_string())),
    }
}

async fn import_run(
    State(state): State<AppState>,
    Json(req): Json<RunReq>,
) -> ApiResult<Json<ImportResp>> {
    let kind = TrackerKind::parse(&req.kind)?;
    let repo = require_repo(&state, &req.repo_id)?;
    let cfg = load_config(&state, kind)?;
    let client = client_for(kind, &cfg)?;

    let items = client.fetch(clamp_limit(req.limit)).await?;
    // Ids are derived from (source, key), so re-importing updates in place
    // instead of accumulating a duplicate row per sync.
    let work_items = to_work_items(&items, &repo);
    state.store.save_work_items(&work_items)?;

    Ok(Json(ImportResp {
        imported: work_items.len() as u32,
        repo_id: repo.0,
    }))
}

#[derive(Debug, Deserialize)]
pub struct FileReq {
    /// Advisory "jira" / "linear"; clear evidence in the file wins.
    pub source: Option<String>,
    pub repo_id: String,
    pub content: String,
}

/// Import from a pasted/uploaded export. This path performs **no network I/O
/// at all** — it exists for air-gapped machines and for anyone who would
/// rather not store a credential.
async fn import_file(
    State(state): State<AppState>,
    Json(req): Json<FileReq>,
) -> ApiResult<Json<ImportResp>> {
    let repo = require_repo(&state, &req.repo_id)?;
    let items = from_export(&req.content, req.source.as_deref())?;
    let work_items = to_work_items(&items, &repo);
    state.store.save_work_items(&work_items)?;
    Ok(Json(ImportResp {
        imported: work_items.len() as u32,
        repo_id: repo.0,
    }))
}
