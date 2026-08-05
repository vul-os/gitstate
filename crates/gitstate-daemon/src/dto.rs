//! Request/response shapes for the JSON API (§3). Domain types serialize to
//! their contract shape directly and are returned as-is; these structs cover
//! the request bodies and the few response envelopes that are not domain
//! types. Field names are snake_case, matching the domain serde.

use serde::{Deserialize, Serialize};

use gitstate_core::{AgentDiffSummary, Hlc, ProjectState, RepoId};

/// `POST /api/repos` body: exactly one of `path` / `remote_url`.
#[derive(Debug, Deserialize)]
pub struct AddRepoReq {
    pub path: Option<String>,
    pub remote_url: Option<String>,
}

/// `POST /api/repos/{id}/scan` body.
#[derive(Debug, Default, Deserialize)]
pub struct ScanReq {
    #[serde(default = "default_true")]
    pub with_forge: bool,
    pub since: Option<String>,
}

fn default_true() -> bool {
    true
}

/// `POST /api/repos/{id}/scan` result.
#[derive(Debug, Serialize)]
pub struct ScanResult {
    pub repo_id: RepoId,
    pub head_sha: String,
    pub commits_scanned: u32,
    pub contributors: u32,
    pub work_items: u32,
    pub project_state: ProjectState,
    pub warnings: Vec<String>,
}

/// `{ "deleted": true }`.
#[derive(Debug, Serialize)]
pub struct DeletedResp {
    pub deleted: bool,
}

/// `{ "ok": true }`.
#[derive(Debug, Serialize)]
pub struct OkResp {
    pub ok: bool,
}

/// `GET /health` body.
#[derive(Debug, Serialize)]
pub struct HealthResp {
    pub status: &'static str,
    pub version: &'static str,
    pub sync: bool,
    pub classifier: &'static str,
}

/// `POST /api/contexts` body. Everything but `name` is optional.
#[derive(Debug, Default, Deserialize)]
pub struct NewContext {
    pub name: String,
    pub description: Option<String>,
    pub repo_ids: Option<Vec<String>>,
    pub pr_refs: Option<Vec<gitstate_core::ContextPrRef>>,
    pub notes: Option<String>,
    pub tags: Option<Vec<String>>,
}

/// `PATCH /api/contexts/{id}` body — all fields optional; a present field
/// replaces that field's value (sets replace wholesale).
#[derive(Debug, Default, Deserialize)]
pub struct ContextPatch {
    pub name: Option<String>,
    pub description: Option<String>,
    pub repo_ids: Option<Vec<String>>,
    pub pr_refs: Option<Vec<gitstate_core::ContextPrRef>>,
    pub notes: Option<String>,
    pub tags: Option<Vec<String>>,
}

/// `POST /api/categories` body.
#[derive(Debug, Deserialize)]
pub struct NewCategory {
    pub key: String,
    pub label: String,
    pub parent_key: Option<String>,
    pub color: Option<String>,
}

/// `PATCH /api/categories/{id}` body.
#[derive(Debug, Default, Deserialize)]
pub struct CategoryPatch {
    pub label: Option<String>,
    pub color: Option<String>,
    pub parent_key: Option<String>,
}

/// `POST /api/classify` and `POST /api/effort` body.
#[derive(Debug, Default, Deserialize)]
pub struct ClassifyReq {
    pub repo_id: String,
    /// Default: all uncategorized items (classify) / all items (effort).
    pub item_ids: Option<Vec<String>>,
}

/// `POST /api/classify/feedback` body.
#[derive(Debug, Deserialize)]
pub struct FeedbackReq {
    pub item_id: String,
    pub category_key: String,
}

/// `POST /api/taxonomy/verify` success.
#[derive(Debug, Serialize)]
pub struct VerifyResp {
    pub valid: bool,
    pub id: String,
}

/// `POST /api/agent-runs` body. Only `goal` is required; every other field is
/// left at its store absence (`None`) when omitted — `ops::log_agent_run`
/// applies no defaults beyond that (mirrors the Go `AgentRunInput`).
#[derive(Debug, Default, Deserialize)]
pub struct NewAgentRun {
    pub goal: String,
    pub repo_id: Option<String>,
    pub pr_id: Option<String>,
    pub issue_id: Option<String>,
    pub supervisor_id: Option<String>,
    pub agent_name: Option<String>,
    pub branch: Option<String>,
    pub diff_summary: Option<AgentDiffSummary>,
    pub tests_passed: Option<bool>,
    /// Raw string, validated against the closed `HumanAction` set in
    /// `ops::log_agent_run` (empty/absent both mean "no verdict yet").
    pub human_action: Option<String>,
    pub iterations: Option<u32>,
    pub cost_usd: Option<f64>,
}

/// `GET /api/agent-runs` query. Every field narrows; all are optional.
#[derive(Debug, Default, Deserialize)]
pub struct AgentRunQuery {
    pub repo_id: Option<String>,
    pub pr_id: Option<String>,
    pub issue_id: Option<String>,
    pub agent_name: Option<String>,
    pub limit: Option<u32>,
}

/// `POST /api/sync/publish` body.
#[derive(Debug, Default, Deserialize)]
pub struct SyncPublishReq {
    pub since: Option<Hlc>,
}

/// `POST /api/sync/publish` success.
#[derive(Debug, Serialize)]
pub struct PublishResp {
    pub published: u32,
}

/// `GET /api/sync/identity` — the two values an operator hands to the other node
/// so it can enrol this one. Never the secret half.
#[derive(Debug, Serialize)]
pub struct NodeIdentityResp {
    /// This node's peer id — the identity its clocks tiebreak on.
    pub peer_id: String,
    /// Hex ed25519 public key.
    pub pubkey: String,
}

/// `POST /api/sync/peers` — a manual enrolment. Both `url` and `pubkey` come
/// from the operator, out of band; there is no field for a discovery hint
/// because there is no discovery.
#[derive(Debug, Deserialize)]
pub struct PeerEnrolReq {
    pub id: String,
    pub url: String,
    pub pubkey: String,
    pub label: Option<String>,
}

/// `POST /api/sync/run` — one round with every enrolled peer.
#[derive(Debug, Default, Serialize)]
pub struct SyncRunResp {
    pub peers: Vec<PeerRoundView>,
}

/// What happened with one peer in one round.
#[derive(Debug, Serialize)]
pub struct PeerRoundView {
    pub peer_id: String,
    pub url: String,
    pub pushed: u32,
    pub pulled: u32,
    pub applied: u32,
    pub skipped: u32,
    pub rejected: u32,
    /// Present when this peer's round failed. The other peers still ran.
    pub error: Option<String>,
}
