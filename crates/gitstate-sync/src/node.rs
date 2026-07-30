//! One round of replication with the manually-enrolled peers.
//!
//! [`SyncNode`] is the thing an operator's `gitstate sync run` drives: for each
//! enrolled peer, push what this node has and pull what that node has. Both
//! directions are needed because either side may be the unreachable one — a node
//! behind NAT can dial a cloud node it cannot be dialled by, and one round still
//! exchanges everything in both directions.
//!
//! There is no scheduler here and no background daemon thread: replication happens
//! when it is asked for. That keeps the failure mode legible (a peer that is down
//! produces one error next to that peer's name) and keeps a node from making
//! outbound connections an operator did not ask for.

use std::sync::Arc;

use gitstate_core::{Hlc, PeerId, Result, Store, SyncIngestResp, SyncPeer};

use crate::ops::{accepted_high_water, ingest_signed, node_identity};
use crate::transport::{HttpPeerClient, PeerClient};

/// What happened with one peer in one round.
#[derive(Debug, Clone)]
pub struct PeerSyncReport {
    pub peer: PeerId,
    pub url: String,
    /// Ops handed to the peer, and what it said it did with them.
    pub pushed: u32,
    pub push_outcome: Option<SyncIngestResp>,
    /// Ops taken from the peer, and what this node did with them.
    pub pulled: u32,
    pub pull_outcome: Option<SyncIngestResp>,
    /// Set when this peer's round failed. The other peers still ran.
    pub error: Option<String>,
}

impl PeerSyncReport {
    fn new(peer: &SyncPeer) -> Self {
        PeerSyncReport {
            peer: peer.id.clone(),
            url: peer.url.clone(),
            pushed: 0,
            push_outcome: None,
            pulled: 0,
            pull_outcome: None,
            error: None,
        }
    }
}

/// Drives replication against the enrolled peer list.
pub struct SyncNode {
    store: Arc<dyn Store>,
    client: Arc<dyn PeerClient>,
    /// This node's public key, so it can be shown to an operator enrolling the
    /// other side.
    public_hex: String,
}

impl SyncNode {
    /// Build over the real HTTP transport, using the identity persisted in the
    /// store (minted on first use).
    pub fn from_store(store: Arc<dyn Store>) -> Result<Self> {
        let identity = node_identity(store.as_ref())?;
        let public_hex = identity.public_hex();
        Ok(SyncNode {
            store,
            client: Arc::new(HttpPeerClient::new(identity)?),
            public_hex,
        })
    }

    /// Build over a supplied transport. Used by the tests that stand a second
    /// node up in-process.
    pub fn with_client(store: Arc<dyn Store>, client: Arc<dyn PeerClient>) -> Result<Self> {
        let public_hex = node_identity(store.as_ref())?.public_hex();
        Ok(SyncNode {
            store,
            client,
            public_hex,
        })
    }

    /// This node's sync public key.
    pub fn public_hex(&self) -> &str {
        &self.public_hex
    }

    /// One round with every enrolled peer. A peer that fails is recorded and the
    /// round continues — one unreachable node must not stop replication with the
    /// rest.
    pub async fn sync_all(&self) -> Result<Vec<PeerSyncReport>> {
        let peers = self.store.list_sync_peers()?;
        let mut reports = Vec::with_capacity(peers.len());
        for peer in &peers {
            reports.push(self.sync_peer(peer).await);
        }
        Ok(reports)
    }

    /// One round with one peer: push, then pull.
    pub async fn sync_peer(&self, peer: &SyncPeer) -> PeerSyncReport {
        let mut report = PeerSyncReport::new(peer);
        match self.push_to(peer).await {
            Ok((n, outcome)) => {
                report.pushed = n;
                report.push_outcome = Some(outcome);
            }
            Err(e) => {
                report.error = Some(e.to_string());
                return report;
            }
        }
        match self.pull_from(peer).await {
            Ok((n, outcome)) => {
                report.pulled = n;
                report.pull_outcome = Some(outcome);
            }
            Err(e) => report.error = Some(e.to_string()),
        }
        report
    }

    /// Hand this node's whole exportable log to `peer`.
    ///
    /// The whole log, not a delta: the receiving side dedups on the op itself and
    /// the merge is idempotent, so re-offering an op it already has costs one row
    /// comparison. Tracking a per-peer *send* cursor would be an optimisation
    /// whose failure mode is a permanently missing op, which is exactly the class
    /// of bug this replication path exists to avoid.
    ///
    /// Each op carries its ORIGINAL author's signature — this node's own for what
    /// it wrote, the third node's for what it relays. See
    /// `Store::signed_ops_since`.
    async fn push_to(&self, peer: &SyncPeer) -> Result<(u32, SyncIngestResp)> {
        let signed = self.store.signed_ops_since(None)?;
        if signed.is_empty() {
            return Ok((0, SyncIngestResp::default()));
        }
        let outcome = self.client.push(peer, &signed).await?;
        Ok((signed.len() as u32, outcome))
    }

    /// Take everything `peer` has, verify each op individually, and record how far
    /// we have got.
    ///
    /// # Why this asks for the WHOLE log, not everything after a cursor
    ///
    /// `sync_ops_since` filters on the op's own clock, and one scalar clock cannot
    /// summarise "what I already have" across several authors. A peer holding an op
    /// from author C at clock 50 and one from author D at clock 100 would push this
    /// node's watermark to 100; C's next op, legitimately stamped 60 because C's
    /// wall clock trails D's, would then be filtered out of every future pull. The
    /// write would be lost permanently with no error anywhere.
    ///
    /// The sound structure is a per-author version vector, which gitstate does not
    /// keep. Until it does, the cursor is **not** used as a filter — it is recorded
    /// for operators to look at, and the transfer is the whole log, deduped and
    /// merged idempotently on arrival. That is the same trade the push direction
    /// makes, for the same reason: a delta whose failure mode is a silently missing
    /// write is not an optimisation worth having.
    async fn pull_from(&self, peer: &SyncPeer) -> Result<(u32, SyncIngestResp)> {
        let signed = self.client.pull(peer, None).await?;
        let outcome = ingest_signed(self.store.as_ref(), &signed)?;

        // Recorded over ops that were actually ACCEPTED — the same admission
        // predicate the ingest used, so a forged op at a high clock cannot move it.
        // Informational only; see above.
        if let Some(high) = accepted_high_water(self.store.as_ref(), &signed)? {
            self.record_progress(peer, &high)?;
        }
        Ok((signed.len() as u32, outcome))
    }

    /// Record the highest clock accepted from a peer, monotonically.
    fn record_progress(&self, peer: &SyncPeer, high: &Hlc) -> Result<()> {
        let forward = match &peer.last_pull_hlc {
            Some(cur) => high > cur,
            None => true,
        };
        if forward {
            self.store.set_sync_peer_cursor(&peer.id, high)?;
        }
        Ok(())
    }
}
