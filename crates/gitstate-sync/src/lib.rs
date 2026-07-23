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
//! full object into its minimal op set; [`apply_op`] ingests a remote op. The
//! [`CrdtSyncEngine`] implements `gitstate_core::SyncEngine` over any
//! [`Store`], appending published/merged ops to the store's op log — the single
//! source of truth that `Store::sync_ops_since` replays.
//!
//! Only "needs a view of strangers you'll never meet" belongs to an optional
//! coordinator; a git tool's own working sets are local + P2P, which is exactly
//! what this crate carries. No cross-population discovery is built here.

mod crdt;
mod ops;
mod transport;

use std::sync::Arc;

use async_trait::async_trait;

use gitstate_core::{
    Error, Hlc, MergeOutcome, PeerId, Result, Store, SyncEngine, SyncOp, SyncStatus,
};

pub use crdt::{op_for_category, op_for_context};
pub use ops::apply_op;
#[cfg(feature = "sync-dmtap")]
pub use transport::DmtapTransport;
pub use transport::{LocalOnlyTransport, Transport};

/// A store-backed CRDT sync engine. `publish` records local ops; `merge`
/// ingests remote ops; both flow through the store's op log so a later
/// `export_since` replays them in HLC order.
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

/// Map a foreign error into the shared error type (kept local so the crate
/// compiles standalone without pulling extra deps).
#[allow(dead_code)]
fn sync_err(msg: impl std::fmt::Display) -> Error {
    Error::Storage(msg.to_string())
}
