//! gitstate-sync — peer-to-peer replication of contexts + categories.
//!
//! # What is here
//!
//! * [`crdt`] — the decomposition of a [`gitstate_core::Context`] /
//!   [`gitstate_core::Category`] into its minimal [`SyncOp`] set, and the merge
//!   rules those ops obey.
//! * [`ops`] — ingest. [`apply_op`] for an op this node already vouches for,
//!   [`ingest_signed`] for anything off the network, which verifies **each op on
//!   its own** before it can touch the store.
//! * [`auth`] — the request token that authenticates a peer connection, the
//!   replay guard that makes it single-use, and the response signature that
//!   authenticates the responder back to the caller.
//! * [`transport`] — [`PeerClient`], and [`HttpPeerClient`]: plain HTTP to a URL
//!   the operator typed.
//! * [`node`] — [`SyncNode`], one round of push-then-pull with each enrolled peer.
//!
//! # What is deliberately NOT here
//!
//! **No discovery.** A peer exists because an operator ran
//! `gitstate sync peer add --url <url> --key <hex>`. There is no directory, no
//! default endpoint, no seed list, no mDNS and no rendezvous broker in any code
//! path, default or fallback. An empty peer list means this node replicates with
//! nobody, which is the right behaviour for a fresh install rather than a
//! degradation.
//!
//! **No NAT traversal, and no dependency on anything that would provide it.** A
//! node with no routable address cannot be dialled; that is IP, not a missing
//! feature. Both directions of a round run over one connection the *caller*
//! opens, so a node behind NAT syncs fully with a node that has an address — which
//! is what the cloud-node deployment path in `docs/DEPLOYMENT.md` is for. If a
//! hole-punching broker is ever added it goes behind [`PeerClient`] as one more
//! implementation, and removing it must leave this default one working.
//!
//! **No optional build feature.** Replication is compiled in unconditionally.
//! There used to be a `sync-dmtap` feature here that linked a crate out of the
//! `envoir` repository and wired nothing to it: both transports it exposed
//! returned `Ok(())` and an empty vector. It has been removed rather than
//! documented, because a seam that carries no traffic is worse than no seam — it
//! reads as a working option in every place it is mentioned.
//!
//! # The merge engine, honestly
//!
//! gitstate's merge decisions are taken by its **own** algebra, implemented over
//! SQLite in `gitstate-store` (`Store::merge_sync_op`). That algebra is not the
//! shared KOTVA engine, and this crate does not claim otherwise.
//!
//! What it does instead is hold itself against that engine: `kotva-sync` is a dev
//! dependency, and `tests/shared_engine_parity.rs` replays the same op streams
//! through both, asserting they select the same winners over every permutation —
//! and naming the one construct (gitstate's resurrecting whole-document tombstone)
//! where the two are *not* equivalent, along with what adopting the shared
//! primitive would change. See that test and [`crdt`] for the full account.

pub mod auth;
mod crdt;
pub mod node;
mod ops;
pub mod transport;

use std::sync::Arc;

use async_trait::async_trait;

use gitstate_core::{Hlc, MergeOutcome, PeerId, Result, Store, SyncEngine, SyncOp, SyncStatus};

pub use crdt::{op_for_category, op_for_context};
pub use node::{PeerSyncReport, SyncNode};
pub use ops::{apply_op, ingest_signed, node_identity, sign_all};
pub use transport::{HttpPeerClient, PeerClient, PULL_PATH, PUSH_PATH};

/// A store-backed CRDT sync engine. `publish` records local ops in the log;
/// `merge` replays remote ops into the rows AND logs them; `export_since`
/// hands the log on in local arrival order, which is enough because the merge
/// is commutative and idempotent.
///
/// This is the *local* half — the algebra and the log. The network half is
/// [`SyncNode`], which is what actually talks to a peer.
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
        // them in the shared log for peers to pull.
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
            peers: self.store.list_sync_peers()?.len() as u32,
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

    /// `status` reports the peers an operator actually enrolled, and zero on a
    /// fresh install. It used to report a hardcoded 0 forever.
    #[tokio::test]
    async fn status_counts_the_enrolled_peers() {
        let store = Arc::new(SqliteStore::open_in_memory().unwrap());
        let e = engine(store.clone());
        assert_eq!(e.status().await.unwrap().peers, 0);

        let node = gitstate_core::NodeIdentity::generate();
        store
            .upsert_sync_peer(&gitstate_core::SyncPeer {
                id: PeerId::from("peer-a"),
                url: "https://peer.example".into(),
                pubkey: node.public_hex(),
                label: None,
                added_at: gitstate_core::ids::now_rfc3339(),
                last_pull_hlc: None,
            })
            .unwrap();
        assert_eq!(e.status().await.unwrap().peers, 1);
    }
}
