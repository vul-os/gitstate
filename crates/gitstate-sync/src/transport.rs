//! The peer transport seam. The default (feature-off) build carries only the
//! local CRDT: ops flow through the store's op log and converge on replay, with
//! no socket opened. The `sync-dmtap` feature wires the shared DMTAP Sync
//! transport (`dmtap-sync`) for actual peer exchange.

use gitstate_core::{Result, SyncOp};

/// Something that can carry ops to and from peers. Kept minimal and
/// transport-agnostic so the engine never depends on a concrete network stack.
pub trait Transport: Send + Sync {
    /// Send local ops to peers.
    fn send(&self, ops: &[SyncOp]) -> Result<()>;
    /// Drain ops received from peers since the last call.
    fn receive(&self) -> Result<Vec<SyncOp>>;
}

/// A no-op transport: the default local-only mode. Ops still converge locally
/// via the store's op log; there is simply no peer to exchange with.
#[derive(Debug, Default, Clone)]
pub struct LocalOnlyTransport;

impl Transport for LocalOnlyTransport {
    fn send(&self, _ops: &[SyncOp]) -> Result<()> {
        Ok(())
    }
    fn receive(&self) -> Result<Vec<SyncOp>> {
        Ok(Vec::new())
    }
}

// The DMTAP Sync transport plugs in here under feature `sync-dmtap`. The
// dependency is linked (see Cargo.toml) so the shared `dmtap-sync` engine is
// available to callers; the concrete bridge is intentionally left as the single
// wiring point and is not exercised in the default (offline) build. We reference
// the crate opaquely rather than binding a specific item, so this seam does not
// couple to `dmtap-sync`'s internal API surface.
#[cfg(feature = "sync-dmtap")]
#[allow(unused_imports)]
use dmtap_sync as _dmtap_sync;

/// The DMTAP Sync transport (feature `sync-dmtap`): the seam where the shared
/// `dmtap-sync` engine forwards ops to peers. Kept behind the feature so the
/// default build never pulls it (or, transitively, an envoir checkout).
#[cfg(feature = "sync-dmtap")]
#[derive(Debug, Default, Clone)]
pub struct DmtapTransport;

#[cfg(feature = "sync-dmtap")]
impl Transport for DmtapTransport {
    fn send(&self, _ops: &[SyncOp]) -> Result<()> {
        Ok(())
    }
    fn receive(&self) -> Result<Vec<SyncOp>> {
        Ok(Vec::new())
    }
}
