//! Ingestion of a single remote [`SyncOp`] into a [`Store`].
//!
//! Two things have to happen when a peer's op arrives, and both happen here (as
//! one transaction, inside the store):
//!
//! 1. the op is **replayed into the typed rows** — a context or category
//!    actually changes — under the merge rules in [`super::crdt`]; and
//! 2. the op is **recorded in the op log**, so this node re-exports it to the
//!    peers it in turn talks to.
//!
//! Step 1 is `Store::merge_sync_op`, implemented by the sqlite store over the
//! per-field clock maps, the OR-Set member clocks and the tombstone clock the
//! schema already carries. It is where the CRDT semantics live: the algebra
//! needs the clocks the rows hold, so re-deriving it out here would duplicate —
//! and risk diverging from — the store's own local write path.
//!
//! Merging is commutative and idempotent, so an op may arrive out of order or
//! more than once and still converge; a duplicate is a no-op for both the rows
//! and the log.

use gitstate_core::{Result, Store, SyncOp};

/// Ingest one remote op: replay it into contexts/categories and record it in
/// the op log. Returns `Ok(true)` if local state changed, `Ok(false)` if the op
/// lost its merge (an older clock than the row already holds) or was a
/// duplicate.
pub fn apply_op(store: &dyn Store, op: &SyncOp) -> Result<bool> {
    store.merge_sync_op(op)
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{ContextId, CtxField, Hlc, PeerId};
    use gitstate_store::SqliteStore;

    fn hlc(wall_ms: u64) -> Hlc {
        Hlc {
            wall_ms,
            counter: 0,
            peer: PeerId::from("peer-a"),
        }
    }

    fn name_op(value: &str, wall_ms: u64) -> SyncOp {
        SyncOp::ContextLww {
            id: ContextId::from("c1"),
            field: CtxField::Name,
            value: value.into(),
            hlc: hlc(wall_ms),
        }
    }

    /// `apply_op` used to append the op to the log and stop, so a merged remote
    /// op left every context and category exactly as it found them. Ingesting
    /// one must now be visible in `get_context`.
    #[test]
    fn applying_a_remote_op_changes_local_state() {
        let store = SqliteStore::open_in_memory().unwrap();
        let id = ContextId::from("c1");
        assert!(store.get_context(&id).unwrap().is_none());

        assert!(apply_op(&store, &name_op("from the peer", 10)).unwrap());
        assert_eq!(
            store.get_context(&id).unwrap().unwrap().name,
            "from the peer",
        );
    }

    /// Re-delivery and out-of-order delivery both converge on the higher clock.
    #[test]
    fn applying_is_idempotent_and_order_independent() {
        let id = ContextId::from("c1");
        for order in [[0usize, 1], [1, 0]] {
            let store = SqliteStore::open_in_memory().unwrap();
            let ops = [name_op("older", 10), name_op("newer", 20)];
            for i in order {
                apply_op(&store, &ops[i]).unwrap();
                // second delivery of the same op is a no-op
                assert!(!apply_op(&store, &ops[i]).unwrap());
            }
            assert_eq!(store.get_context(&id).unwrap().unwrap().name, "newer");
        }
    }
}
