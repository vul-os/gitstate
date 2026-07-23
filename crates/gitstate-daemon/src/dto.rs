//! Request/response shapes for the JSON API (§3). Domain types serialize to
//! their contract shape directly and are returned as-is; these structs cover
//! the request bodies and the few response envelopes that are not domain
//! types. Field names are snake_case, matching the domain serde.

use serde::{Deserialize, Serialize};

use gitstate_core::{Hlc, ProjectState, RepoId};

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
