//! Ingestion of a single remote [`SyncOp`] into a [`Store`].
//!
//! The store's op log is the CRDT source of truth: `Store::upsert_context` /
//! `upsert_category` already decompose local edits into ops (§5) and the same
//! log is what `Store::sync_ops_since` replays. [`apply_op`] records a remote op
//! into that log so a subsequent replay converges. It is idempotent at the
//! log level (appending the same op twice is a no-op for convergence, since
//! replay is HLC-ordered and each op's effect is a function of its clock).
//!
//! Reconstructing typed rows from the merged log is the store's responsibility;
//! this function deliberately does not re-derive row state itself, which would
//! duplicate — and risk diverging from — the store's own merge.

use gitstate_core::{Result, Store, SyncOp};

/// Ingest one remote op. Returns `Ok(true)` if it was recorded (a candidate
/// state change), `Ok(false)` if it was a no-op.
pub fn apply_op(store: &dyn Store, op: &SyncOp) -> Result<bool> {
    store.append_sync_ops(std::slice::from_ref(op))?;
    Ok(true)
}
