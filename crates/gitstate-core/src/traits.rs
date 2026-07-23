//! The four seams implemented by the adapter crates: forge, classifier, store,
//! sync. Domain code depends only on these traits, never on git2/rusqlite/gh.

use crate::domain::*;
use crate::ids::*;
use crate::taxonomy::Taxonomy;
use crate::Result;
use async_trait::async_trait;

/// A forge (GitHub/GitLab) as seen through the `gh`/`glab` CLI or REST.
#[async_trait]
pub trait ForgeClient: Send + Sync {
    fn forge(&self) -> Forge;
    async fn list_pull_requests(&self, slug: &str, since: Option<&str>) -> Result<Vec<WorkItem>>;
    async fn list_issues(&self, slug: &str, since: Option<&str>) -> Result<Vec<WorkItem>>;
    /// Reviews, materialized as `WorkItem`s of kind `Review`.
    async fn list_reviews(&self, slug: &str, since: Option<&str>) -> Result<Vec<WorkItem>>;
    /// Map a remote URL to "owner/name".
    async fn resolve_slug(&self, remote_url: &str) -> Result<String>;
}

/// What a classifier can do.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ClassifierCapability {
    Llm,
    Heuristic,
}

impl ClassifierCapability {
    pub fn as_str(&self) -> &'static str {
        match self {
            ClassifierCapability::Llm => "llm",
            ClassifierCapability::Heuristic => "heuristic",
        }
    }
}

/// Classifies work items into taxonomy categories and judges diff difficulty.
#[async_trait]
pub trait Classifier: Send + Sync {
    fn capability(&self) -> ClassifierCapability;
    async fn classify(
        &self,
        items: &[WorkItem],
        taxonomy: &Taxonomy,
    ) -> Result<Vec<Classification>>;
    async fn judge_effort(&self, diffs: &[DiffSummary]) -> Result<Vec<EffortEstimate>>;
}

/// Local persistence. Synchronous — SQLite behind a mutex; no async needed.
pub trait Store: Send + Sync {
    // repos
    fn upsert_repo(&self, repo: &Repo) -> Result<()>;
    fn get_repo(&self, id: &RepoId) -> Result<Option<Repo>>;
    fn list_repos(&self) -> Result<Vec<Repo>>;
    fn delete_repo(&self, id: &RepoId) -> Result<()>;
    // contributors
    fn upsert_contributor(&self, c: &Contributor) -> Result<()>;
    fn list_contributors(&self) -> Result<Vec<Contributor>>;
    // derived caches
    fn save_commits(&self, repo: &RepoId, commits: &[Commit]) -> Result<()>;
    fn save_project_state(&self, s: &ProjectState) -> Result<()>;
    fn get_project_state(&self, repo: &RepoId) -> Result<Option<ProjectState>>;
    fn save_contributions(&self, rows: &[Contribution]) -> Result<()>;
    fn get_contributions(&self, repo: &RepoId, from: &str, to: &str) -> Result<Vec<Contribution>>;
    fn save_work_items(&self, items: &[WorkItem]) -> Result<()>;
    fn list_work_items(&self, repo: &RepoId) -> Result<Vec<WorkItem>>;
    fn save_effort(&self, rows: &[EffortEstimate]) -> Result<()>;
    fn save_classifications(&self, rows: &[Classification]) -> Result<()>;
    fn get_classification(&self, item: &WorkItemId) -> Result<Option<Classification>>;
    // contexts (CRDT-backed)
    fn upsert_context(&self, c: &Context) -> Result<()>;
    fn get_context(&self, id: &ContextId) -> Result<Option<Context>>;
    /// Excludes tombstoned contexts.
    fn list_contexts(&self) -> Result<Vec<Context>>;
    // categories (CRDT-backed)
    fn upsert_category(&self, c: &Category) -> Result<()>;
    fn get_category(&self, key: &str) -> Result<Option<Category>>;
    fn list_categories(&self) -> Result<Vec<Category>>;
    // personalization + kv + sync log
    fn record_feedback(&self, item: &WorkItemId, chosen_key: &str) -> Result<()>;
    fn kv_get(&self, k: &str) -> Result<Option<String>>;
    fn kv_set(&self, k: &str, v: &str) -> Result<()>;
    fn append_sync_ops(&self, ops: &[SyncOp]) -> Result<()>;
    fn sync_ops_since(&self, since: Option<&Hlc>) -> Result<Vec<SyncOp>>;
}

/// The outcome of merging a batch of remote ops.
#[derive(Debug, Clone, Copy, Default, serde::Serialize, serde::Deserialize)]
pub struct MergeOutcome {
    pub applied: u32,
    pub skipped: u32,
    pub conflicts_resolved: u32,
}

/// The sync engine's view of the world.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct SyncStatus {
    pub enabled: bool,
    pub peer_id: PeerId,
    pub peers: u32,
    pub last_op_hlc: Option<Hlc>,
}

/// Peer-to-peer replication of contexts + categories (optional feature).
#[async_trait]
pub trait SyncEngine: Send + Sync {
    fn peer_id(&self) -> PeerId;
    /// Broadcast local ops.
    async fn publish(&self, ops: &[SyncOp]) -> Result<()>;
    /// Apply remote ops (idempotent).
    async fn merge(&self, ops: &[SyncOp]) -> Result<MergeOutcome>;
    async fn export_since(&self, since: Option<Hlc>) -> Result<Vec<SyncOp>>;
    async fn status(&self) -> Result<SyncStatus>;
}
