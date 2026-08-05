//! `/api/agent-runs` — the agent-run write path (T11 port plan, wave 1 / §4).
//!
//! Gated by the same `require_admin` management-API gate as every other
//! route in this file's `Router` (see `router::build_router`): a logged run
//! is exactly as sensitive as the rest of what this node knows (which repos
//! exist, what a saved context contains), so it gets no separate scope or
//! token of its own. The Go server's per-token `write:agent_runs` scope had a
//! reason to exist there (many API tokens, one shared server); it has no
//! counterpart to guard here, because there is only one operator and one
//! gate for everything they're allowed to see or change.

use axum::extract::{Query, State};
use axum::http::StatusCode;
use axum::routing::get;
use axum::{Json, Router};

use gitstate_core::AgentRun;

use super::ApiResult;
use crate::dto::{AgentRunQuery, NewAgentRun};
use crate::ops;
use crate::state::AppState;

pub fn agent_run_routes() -> Router<AppState> {
    Router::new().route("/api/agent-runs", get(list).post(create))
}

async fn list(
    State(state): State<AppState>,
    Query(q): Query<AgentRunQuery>,
) -> ApiResult<Json<Vec<AgentRun>>> {
    Ok(Json(ops::list_agent_runs(&state, q)?))
}

async fn create(
    State(state): State<AppState>,
    Json(req): Json<NewAgentRun>,
) -> ApiResult<(StatusCode, Json<AgentRun>)> {
    let run = ops::log_agent_run(&state, req)?;
    Ok((StatusCode::CREATED, Json(run)))
}
