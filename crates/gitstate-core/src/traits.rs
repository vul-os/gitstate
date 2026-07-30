//! The four seams implemented by the adapter crates: forge, classifier, store,
//! sync. Domain code depends only on these traits, never on git2/rusqlite/gh.

use crate::domain::*;
use crate::ids::*;
use crate::peer::{SignedOp, SyncPeer};
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
    /// Cached commits, oldest first. `repo: None` spans every repo — the
    /// analytics rollups read across the whole local ledger.
    fn list_commits(&self, repo: Option<&RepoId>) -> Result<Vec<Commit>>;
    /// Every work item across every repo, newest first.
    fn list_all_work_items(&self) -> Result<Vec<WorkItem>>;
    fn save_effort(&self, rows: &[EffortEstimate]) -> Result<()>;
    fn save_classifications(&self, rows: &[Classification]) -> Result<()>;
    fn get_classification(&self, item: &WorkItemId) -> Result<Option<Classification>>;
    /// Every stored classification for a repo's work items. Read-only: unlike
    /// running the classifier, this judges nothing and writes nothing.
    fn list_classifications(&self, repo: &RepoId) -> Result<Vec<Classification>>;
    /// Every stored effort estimate for a repo's work items. Read-only.
    fn list_effort(&self, repo: &RepoId) -> Result<Vec<EffortEstimate>>;
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
    /// Append ops to the log *without* replaying them into rows. This is the
    /// local-publish path: `upsert_context` / `upsert_category` have already
    /// written the rows, and these ops are their record for peers to pull.
    /// To ingest a REMOTE op, use [`Store::merge_sync_op`] — appending alone
    /// changes no context or category.
    fn append_sync_ops(&self, ops: &[SyncOp]) -> Result<()>;
    /// Ingest one remote op **whose authorship this node has already verified or
    /// vouches for locally** (an `import`, a test, a replay of our own log):
    /// replay it into the typed context/category rows under the CRDT merge rules
    /// (per-field LWW by `Hlc`, add-wins OR-Set for members, whole-doc tombstone)
    /// **and** record it in the op log, in one transaction.
    ///
    /// The log entry carries no signature, so the op is **not re-exportable to
    /// peers** unless this node authored it. Use [`Store::merge_signed_sync_op`]
    /// for anything that came off the network.
    ///
    /// Returns `true` if local state changed and `false` if the op lost its
    /// merge (an older clock than what the row already holds) or was a
    /// duplicate. Merging is commutative and idempotent, so a peer's ops may
    /// arrive in any order, more than once, and still converge.
    fn merge_sync_op(&self, op: &SyncOp) -> Result<bool>;
    /// As [`Store::merge_sync_op`], but records the author and signature the op
    /// arrived with so this node can **relay it verbatim**. The caller must have
    /// verified the signature already (`gitstate_sync::ingest_signed` does).
    ///
    /// Storing the original author's signature rather than re-signing on each hop
    /// is what makes a three-node topology trustworthy: the third node's change
    /// carries the third node's attestation all the way, so nobody has to take the
    /// middle node's word for who wrote it.
    fn merge_signed_sync_op(&self, signed: &SignedOp) -> Result<bool>;
    /// Ops from the log, in local arrival order (see the implementation's note
    /// on why arrival order is sufficient), skipping any whose clock is at or
    /// below `since`.
    fn sync_ops_since(&self, since: Option<&Hlc>) -> Result<Vec<SyncOp>>;
    /// The exportable log: every op paired with an attestation of its author.
    ///
    /// A row that this node authored is signed here and now with this node's key
    /// (ed25519 is deterministic, so the same op yields the same bytes every
    /// time). A row that arrived from a peer carries that peer's stored signature
    /// verbatim.
    ///
    /// A row that is neither — an op merged through the unsigned local path, e.g.
    /// `context import` of somebody else's file — is **omitted**, because this
    /// node cannot honestly attest to authorship it does not hold and must not
    /// forge an attribution to make the export look complete. Under-sharing is
    /// the fail-closed direction: the op still converges wherever it was
    /// legitimately signed.
    fn signed_ops_since(&self, since: Option<&Hlc>) -> Result<Vec<SignedOp>>;

    // ── manually-enrolled peers (see `crate::peer`) ──
    /// This node's sync identity, minted on first use and persisted locally.
    /// Returns the hex secret seed; the caller derives the public half.
    fn node_secret_hex(&self) -> Result<String>;
    /// Enrol or update a peer. Keyed on `id`; the unique index on `pubkey`
    /// refuses a second enrolment of the same key under a different id.
    fn upsert_sync_peer(&self, peer: &SyncPeer) -> Result<()>;
    /// Every enrolled peer. Empty on a fresh install — there is no discovery, so
    /// nothing populates this but an operator.
    fn list_sync_peers(&self) -> Result<Vec<SyncPeer>>;
    /// Look a peer up by the hex public key that signed an op. This is the
    /// admission check: a key with no row here is not an authorised author.
    fn sync_peer_by_pubkey(&self, pubkey: &str) -> Result<Option<SyncPeer>>;
    /// Remove an enrolment. Returns whether a row was removed.
    fn delete_sync_peer(&self, id: &PeerId) -> Result<bool>;
    /// Record how far this node has pulled from a peer.
    fn set_sync_peer_cursor(&self, id: &PeerId, hlc: &Hlc) -> Result<()>;
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
