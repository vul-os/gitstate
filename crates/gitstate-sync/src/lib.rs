//! gitstate-sync — peer-to-peer replication of contexts + categories.
//!
//! This crate is **excluded** from the default workspace (see the root
//! `Cargo.toml`) so a bare `cargo build` of gitstate never touches the optional
//! `dmtap-sync` transport (and, transitively, an `envoir` checkout). Build it on
//! its own:
//!
//! ```sh
//! cargo build --manifest-path crates/gitstate-sync/Cargo.toml               # local CRDT only
//! cargo build --manifest-path crates/gitstate-sync/Cargo.toml --features sync-dmtap
//! ```
//!
//! # What is (and is not) here
//!
//! The CRDT algebra for gitstate's two sharable objects — the [`Context`] and
//! the [`Category`] — expressed as the shared [`SyncOp`] envelope from
//! `gitstate-core` (§5). [`op_for_context`] / [`op_for_category`] decompose a
//! full object into its minimal op set; [`apply_op`] ingests a remote op —
//! replaying it into the context/category rows and recording it in the op log,
//! atomically. The [`CrdtSyncEngine`] implements `gitstate_core::SyncEngine`
//! over any [`Store`]: `publish` records local ops (their rows were already
//! written by the store's own edit path), `merge` ingests remote ones, and
//! `export_since` hands the log to the next peer.
//!
//! Only "needs a view of strangers you'll never meet" belongs to an optional
//! coordinator; a git tool's own working sets are local + P2P, which is exactly
//! what this crate carries. No cross-population discovery is built here.

mod crdt;
mod ops;
mod transport;

use std::sync::Arc;

use async_trait::async_trait;

use gitstate_core::{Hlc, MergeOutcome, PeerId, Result, Store, SyncEngine, SyncOp, SyncStatus};

pub use crdt::{op_for_category, op_for_context};
pub use ops::apply_op;
#[cfg(feature = "sync-dmtap")]
pub use transport::DmtapTransport;
pub use transport::{LocalOnlyTransport, Transport};

/// A store-backed CRDT sync engine. `publish` records local ops in the log;
/// `merge` replays remote ops into the rows AND logs them; `export_since`
/// hands the log on in local arrival order, which is enough because the merge
/// is commutative and idempotent.
pub struct CrdtSyncEngine {
    peer: PeerId,
    store: Arc<dyn Store>,
}

impl CrdtSyncEngine {
    pub fn new(peer: PeerId, store: Arc<dyn Store>) -> Self {
        CrdtSyncEngine { peer, store }
    }

    /// Build with the peer id persisted in the store's `kv` (`peer_id`), or a
    /// fresh one if unset.
    pub fn from_store(store: Arc<dyn Store>) -> Result<Self> {
        let peer = store
            .kv_get("peer_id")?
            .filter(|s| !s.is_empty())
            .map(PeerId::from)
            .unwrap_or_else(PeerId::new);
        Ok(CrdtSyncEngine { peer, store })
    }
}

#[async_trait]
impl SyncEngine for CrdtSyncEngine {
    fn peer_id(&self) -> PeerId {
        self.peer.clone()
    }

    async fn publish(&self, ops: &[SyncOp]) -> Result<()> {
        // Local ops are already applied to rows by the Store; publishing records
        // them in the shared log for peers to pull. A wired transport (feature
        // `sync-dmtap`) additionally forwards them — see `transport.rs`.
        self.store.append_sync_ops(ops)
    }

    async fn merge(&self, ops: &[SyncOp]) -> Result<MergeOutcome> {
        let mut out = MergeOutcome::default();
        for op in ops {
            match apply_op(self.store.as_ref(), op) {
                Ok(true) => out.applied += 1,
                Ok(false) => out.skipped += 1,
                Err(_) => out.skipped += 1,
            }
        }
        Ok(out)
    }

    async fn export_since(&self, since: Option<Hlc>) -> Result<Vec<SyncOp>> {
        self.store.sync_ops_since(since.as_ref())
    }

    async fn status(&self) -> Result<SyncStatus> {
        let ops = self.store.sync_ops_since(None)?;
        let last = ops.iter().map(|o| o.hlc().clone()).max();
        Ok(SyncStatus {
            enabled: true,
            peer_id: self.peer.clone(),
            peers: 0,
            last_op_hlc: last,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{Context, ContextId, ContextPrRef, RepoId};
    use gitstate_store::SqliteStore;

    fn engine(store: Arc<dyn Store>) -> CrdtSyncEngine {
        CrdtSyncEngine::new(PeerId::from("test-peer"), store)
    }

    /// The end-to-end claim this crate makes: hand peer B's exported log to
    /// peer A's `merge` and peer A now HAS the object. Before the replay landed
    /// this passed the ops around and changed nothing on the receiving side.
    #[tokio::test]
    async fn merging_a_peers_log_reproduces_the_object_locally() {
        let author = Arc::new(SqliteStore::open_in_memory().unwrap());
        let receiver: Arc<dyn Store> = Arc::new(SqliteStore::open_in_memory().unwrap());

        let ctx = Context {
            id: ContextId::from("c1"),
            name: "Q3 refactor".into(),
            description: "cleanup".into(),
            repo_ids: vec![RepoId::from("r1")],
            pr_refs: vec![ContextPrRef {
                repo_slug: "vul-os/gitstate".into(),
                number: 7,
                note: None,
            }],
            notes: "notes".into(),
            tags: vec!["refactor".into()],
            created_at: gitstate_core::ids::now_rfc3339(),
            updated_at: gitstate_core::ids::now_rfc3339(),
            hlc: Hlc {
                wall_ms: 0,
                counter: 0,
                peer: PeerId::from(""),
            },
            deleted: false,
        };
        author.upsert_context(&ctx).unwrap();

        let from_author = engine(author.clone());
        let ops = from_author.export_since(None).await.unwrap();
        assert!(!ops.is_empty(), "the author must have something to publish");

        let out = engine(receiver.clone()).merge(&ops).await.unwrap();
        assert_eq!(out.applied as usize, ops.len(), "every op applied");
        assert_eq!(out.skipped, 0);

        let landed = receiver.get_context(&ctx.id).unwrap().unwrap();
        assert_eq!(landed.name, "Q3 refactor");
        assert_eq!(landed.description, "cleanup");
        assert_eq!(landed.notes, "notes");
        assert_eq!(landed.tags, vec!["refactor".to_string()]);
        assert_eq!(landed.repo_ids, vec![RepoId::from("r1")]);
        assert_eq!(landed.pr_refs.len(), 1);
        assert!(!landed.deleted);
        assert_eq!(receiver.list_contexts().unwrap().len(), 1);
    }

    /// Merging the same batch twice reports zero further changes and leaves the
    /// state alone.
    #[tokio::test]
    async fn merging_the_same_batch_twice_is_a_no_op() {
        let author = Arc::new(SqliteStore::open_in_memory().unwrap());
        let receiver: Arc<dyn Store> = Arc::new(SqliteStore::open_in_memory().unwrap());
        author
            .upsert_context(&Context {
                id: ContextId::from("c1"),
                name: "one".into(),
                description: String::new(),
                repo_ids: vec![],
                pr_refs: vec![],
                notes: String::new(),
                tags: vec![],
                created_at: gitstate_core::ids::now_rfc3339(),
                updated_at: gitstate_core::ids::now_rfc3339(),
                hlc: Hlc {
                    wall_ms: 0,
                    counter: 0,
                    peer: PeerId::from(""),
                },
                deleted: false,
            })
            .unwrap();

        let ops = engine(author).export_since(None).await.unwrap();
        let recv = engine(receiver.clone());
        let first = recv.merge(&ops).await.unwrap();
        let second = recv.merge(&ops).await.unwrap();
        assert!(first.applied > 0);
        assert_eq!(second.applied, 0, "a replayed batch applies nothing new");
        assert_eq!(second.skipped as usize, ops.len());
        assert_eq!(
            receiver
                .get_context(&ContextId::from("c1"))
                .unwrap()
                .unwrap()
                .name,
            "one"
        );
    }
}
