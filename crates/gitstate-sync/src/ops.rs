//! Ingestion of a remote [`SyncOp`] into a [`Store`] — the trusted-local path
//! ([`apply_op`]) and the from-the-network path ([`ingest_signed`]).
//!
//! Two things have to happen when a peer's op arrives, and both happen inside
//! one store transaction:
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
//! and the log. That is a proven property, not an aspiration —
//! `tests/convergence.rs` drives every permutation of a mixed op set through
//! this function and asserts one final state.
//!
//! # Why there are two entry points
//!
//! [`apply_op`] takes an op that is already known-good: it came off this node's
//! own log, out of an operator's `context import`, or out of a test. It performs
//! **no** authentication, and callers on a network path must not use it.
//!
//! [`ingest_signed`] is the network path. It verifies each op **individually**
//! against the enrolled peer list before that op is allowed anywhere near the
//! store: signature, author admission, clock-identity binding and skew (see
//! `gitstate_core::peer`). Verifying per op rather than per connection is the
//! point — ops are relayed, so a node re-exports changes it did not author, and
//! "the connection authenticated" tells you nothing about who wrote the change
//! inside it.

use gitstate_core::{
    now_wall_ms, Error, NodeIdentity, Result, SignedOp, Store, SyncIngestResp, SyncOp,
};

/// Ingest one already-trusted op: replay it into contexts/categories and record
/// it in the op log. Returns `Ok(true)` if local state changed, `Ok(false)` if
/// the op lost its merge (an older clock than the row already holds) or was a
/// duplicate.
///
/// Performs no authentication. For anything that arrived over a socket, use
/// [`ingest_signed`].
pub fn apply_op(store: &dyn Store, op: &SyncOp) -> Result<bool> {
    store.merge_sync_op(op)
}

/// Verify and ingest a batch that arrived from the network.
///
/// Each op is checked on its own against the peer enrolled for the key that
/// signed it. An op whose author is not enrolled, whose signature does not
/// verify, whose clock claims another node's identity, or whose clock is beyond
/// the skew bound is **rejected and not stored** — it does not reach the rows and
/// it does not reach the log, so this node cannot relay it onward either.
///
/// A rejection does not abort the batch: one bad op among fifty good ones must
/// not cost the good ones, and the count comes back in
/// [`SyncIngestResp::rejected`] so an operator can see it happened. What a
/// rejection never does is degrade into acceptance.
///
/// # This node's own ops, coming back
///
/// A peer re-exports its whole log, which includes everything this node pushed to
/// it, so most of a steady-state pull is our own writes returning. Those are
/// admitted under this node's own key and then merge as duplicates — counted as
/// `skipped`, which is what they are.
///
/// They must not be counted as `rejected`. `rejected` is the number an operator is
/// meant to read as "somebody sent me something that failed verification", and a
/// value that is nonzero on every healthy round destroys that signal completely —
/// the same way a warning that always fires stops being a warning.
pub fn ingest_signed(store: &dyn Store, ops: &[SignedOp]) -> Result<SyncIngestResp> {
    let now = now_wall_ms();
    // This node as an "enrolled peer": we hold the secret for this key, so an op
    // signed by it and stamped with our clock identity is ours by construction. It
    // still goes through the SAME `verify_from` as a remote peer's — a forged op
    // claiming our key would fail the signature, and one claiming our key with
    // somebody else's clock identity would fail the identity check.
    let myself = local_peer(store).ok();
    let mut out = SyncIngestResp::default();
    for signed in ops {
        if !is_admitted(store, myself.as_ref(), signed, now) {
            out.rejected += 1;
            continue;
        }
        // The author's own signature is stored with the op, so this node relays
        // the original attestation instead of re-signing on the way out.
        match store.merge_signed_sync_op(signed) {
            Ok(true) => out.applied += 1,
            Ok(false) => out.skipped += 1,
            Err(_) => out.rejected += 1,
        }
    }
    Ok(out)
}

/// The single admission predicate: is this op allowed to touch local state?
///
/// One function so a cursor cannot be advanced over an op the ingest would have
/// refused — which would skip past a write that had not really arrived. Every
/// caller asks this exact question.
fn is_admitted(
    store: &dyn Store,
    myself: Option<&gitstate_core::SyncPeer>,
    signed: &SignedOp,
    now: u64,
) -> bool {
    let peer = match myself.filter(|me| me.pubkey == signed.author) {
        Some(me) => me.clone(),
        None => match store.sync_peer_by_pubkey(&signed.author) {
            Ok(Some(p)) => p,
            // Unknown author, or a store hiccup while looking it up: either way
            // this op is not admitted. Failing closed on the error branch is
            // deliberate — a lookup that could not confirm admission has not
            // confirmed it.
            Ok(None) | Err(_) => return false,
        },
    };
    signed.verify_from(&peer, now).is_ok()
}

/// The highest clock among the ops in `ops` that this node **would accept**.
///
/// This is what a per-peer pull cursor may be advanced to, and only this. Taking
/// the maximum over the whole batch instead would move the watermark past a
/// rejected op, and if that op was a forgery of a write that has not yet arrived,
/// the real one would then be filtered out on every future pull — a permanently
/// lost write caused by an attacker sending one bad message.
pub fn accepted_high_water(
    store: &dyn Store,
    ops: &[SignedOp],
) -> Result<Option<gitstate_core::Hlc>> {
    let now = now_wall_ms();
    let myself = local_peer(store).ok();
    Ok(ops
        .iter()
        .filter(|s| is_admitted(store, myself.as_ref(), s, now))
        .map(|s| s.op.hlc().clone())
        .max())
}

/// Sign every op in `ops` with this node's identity, ready to hand to a peer.
pub fn sign_all(identity: &NodeIdentity, ops: &[SyncOp]) -> Result<Vec<SignedOp>> {
    ops.iter().map(|op| identity.sign_op(op)).collect()
}

/// Load (minting on first use) this node's sync identity from the store.
pub fn node_identity(store: &dyn Store) -> Result<NodeIdentity> {
    let secret = store.node_secret_hex()?;
    NodeIdentity::from_secret_hex(&secret)
        .map_err(|e| Error::invalid(format!("stored sync identity is unusable: {e}")))
}

/// This node described the way a peer would describe it: its clock identity and
/// its public key. `url` is empty because nobody dials themselves.
///
/// Errors when this node has no `peer_id` yet, which means it has authored nothing
/// — so there is no own-op case to recognise, and the caller correctly falls back
/// to the enrolled-peer lookup alone.
pub fn local_peer(store: &dyn Store) -> Result<gitstate_core::SyncPeer> {
    let id = store
        .kv_get("peer_id")?
        .filter(|s| !s.is_empty())
        .ok_or_else(|| Error::invalid("this node has no peer id yet"))?;
    Ok(gitstate_core::SyncPeer {
        id: gitstate_core::PeerId::from(id.as_str()),
        url: String::new(),
        pubkey: node_identity(store)?.public_hex(),
        label: Some("this node".into()),
        added_at: gitstate_core::now_rfc3339(),
        last_pull_hlc: None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{now_rfc3339, ContextId, CtxField, Hlc, PeerId, SyncPeer};
    use gitstate_store::SqliteStore;

    fn hlc(wall_ms: u64, peer: &str) -> Hlc {
        Hlc {
            wall_ms,
            counter: 0,
            peer: PeerId::from(peer),
        }
    }

    fn name_op(value: &str, wall_ms: u64, peer: &str) -> SyncOp {
        SyncOp::ContextLww {
            id: ContextId::from("c1"),
            field: CtxField::Name,
            value: value.into(),
            hlc: hlc(wall_ms, peer),
        }
    }

    fn enrol(store: &SqliteStore, id: &str, node: &NodeIdentity) -> SyncPeer {
        let peer = SyncPeer {
            id: PeerId::from(id),
            url: "https://peer.example".into(),
            pubkey: node.public_hex(),
            label: None,
            added_at: now_rfc3339(),
            last_pull_hlc: None,
        };
        store.upsert_sync_peer(&peer).unwrap();
        peer
    }

    /// `apply_op` used to append the op to the log and stop, so a merged remote
    /// op left every context and category exactly as it found them. Ingesting
    /// one must be visible in `get_context`.
    #[test]
    fn applying_a_remote_op_changes_local_state() {
        let store = SqliteStore::open_in_memory().unwrap();
        let id = ContextId::from("c1");
        assert!(store.get_context(&id).unwrap().is_none());

        assert!(apply_op(&store, &name_op("from the peer", 10, "peer-a")).unwrap());
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
            let ops = [
                name_op("older", 10, "peer-a"),
                name_op("newer", 20, "peer-a"),
            ];
            for i in order {
                apply_op(&store, &ops[i]).unwrap();
                // second delivery of the same op is a no-op
                assert!(!apply_op(&store, &ops[i]).unwrap());
            }
            assert_eq!(store.get_context(&id).unwrap().unwrap().name, "newer");
        }
    }

    #[test]
    fn a_signed_op_from_an_enrolled_peer_is_applied() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        enrol(&store, "peer-a", &author);

        let signed = sign_all(&author, &[name_op("from the peer", 10, "peer-a")]).unwrap();
        let out = ingest_signed(&store, &signed).unwrap();
        assert_eq!((out.applied, out.skipped, out.rejected), (1, 0, 0));
        assert_eq!(
            store
                .get_context(&ContextId::from("c1"))
                .unwrap()
                .unwrap()
                .name,
            "from the peer"
        );
    }

    /// The whole point of signing each op: a batch that arrives over an
    /// authenticated connection is still verified op by op, so one forged op
    /// inside it is dropped while the honest ones land.
    #[test]
    fn a_forged_op_inside_an_honest_batch_is_dropped_and_never_stored() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        let stranger = NodeIdentity::generate();
        enrol(&store, "peer-a", &author);

        let mut batch = sign_all(&author, &[name_op("honest", 10, "peer-a")]).unwrap();
        batch.extend(
            sign_all(
                &stranger,
                &[SyncOp::ContextLww {
                    id: ContextId::from("c2"),
                    field: CtxField::Name,
                    value: "forged".into(),
                    hlc: hlc(99, "peer-a"),
                }],
            )
            .unwrap(),
        );

        let out = ingest_signed(&store, &batch).unwrap();
        assert_eq!((out.applied, out.skipped, out.rejected), (1, 0, 1));
        assert!(
            store.get_context(&ContextId::from("c2")).unwrap().is_none(),
            "the forged op must not reach the rows"
        );
        // Nor the log — otherwise this node would relay it to its own peers.
        let logged = store.sync_ops_since(None).unwrap();
        assert_eq!(logged.len(), 1, "only the honest op is logged: {logged:?}");
    }

    /// A tampered op inside an otherwise valid batch fails its own signature.
    #[test]
    fn a_tampered_op_is_rejected() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        enrol(&store, "peer-a", &author);

        let mut signed = sign_all(&author, &[name_op("original", 10, "peer-a")]).unwrap();
        signed[0].op = name_op("rewritten in flight", 10, "peer-a");
        let out = ingest_signed(&store, &signed).unwrap();
        assert_eq!(out.rejected, 1);
        assert!(store.get_context(&ContextId::from("c1")).unwrap().is_none());
    }

    /// With nobody enrolled, nothing is admitted. An empty peer list is a closed
    /// door, not an open one.
    #[test]
    fn nothing_is_admitted_when_no_peer_is_enrolled() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        let signed = sign_all(&author, &[name_op("x", 10, "peer-a")]).unwrap();
        let out = ingest_signed(&store, &signed).unwrap();
        assert_eq!((out.applied, out.rejected), (0, 1));
    }

    /// An op stamped at the end of time would win every field forever, so it is
    /// refused at the boundary — even from a correctly-enrolled, correctly-signing
    /// peer.
    #[test]
    fn a_far_future_clock_from_an_enrolled_peer_is_refused() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        enrol(&store, "peer-a", &author);
        let signed = sign_all(&author, &[name_op("end of time", u64::MAX, "peer-a")]).unwrap();
        let out = ingest_signed(&store, &signed).unwrap();
        assert_eq!(out.rejected, 1);
        assert!(store.get_context(&ContextId::from("c1")).unwrap().is_none());
    }

    /// A peer re-exports everything it was given, so our own ops come straight
    /// back. They are duplicates, and must be counted as such: a `rejected` count
    /// that is nonzero on every healthy round is a signal an operator learns to
    /// ignore, which is worse than not reporting it.
    #[test]
    fn our_own_ops_coming_back_from_a_peer_are_skipped_not_rejected() {
        let store = SqliteStore::open_in_memory().unwrap();
        store.kv_set("peer_id", "me").unwrap();
        let me = node_identity(&store).unwrap();

        // A local write, then the exportable log — which is what a peer would have
        // stored and would hand straight back on the next pull.
        store
            .upsert_context(&gitstate_core::Context {
                id: ContextId::from("c1"),
                name: "mine".into(),
                description: String::new(),
                repo_ids: vec![],
                pr_refs: vec![],
                notes: String::new(),
                tags: vec![],
                created_at: now_rfc3339(),
                updated_at: now_rfc3339(),
                hlc: hlc(0, ""),
                deleted: false,
            })
            .unwrap();
        let mine = store.signed_ops_since(None).unwrap();
        assert!(!mine.is_empty());
        assert!(mine.iter().all(|s| s.author == me.public_hex()));

        let out = ingest_signed(&store, &mine).unwrap();
        assert_eq!(out.rejected, 0, "our own ops are not forgeries");
        assert_eq!(
            out.skipped as usize,
            mine.len(),
            "they are duplicates: {out:?}"
        );

        // And an op that CLAIMS our key without holding it is still refused.
        let impostor = NodeIdentity::generate();
        let mut forged = impostor.sign_op(&name_op("not mine", 5_000, "me")).unwrap();
        forged.author = me.public_hex();
        let out = ingest_signed(&store, &[forged]).unwrap();
        assert_eq!(out.rejected, 1, "a forged claim on our key is refused");
    }

    /// The cursor may only advance over ops that were actually admitted.
    #[test]
    fn the_high_water_mark_ignores_ops_that_would_be_rejected() {
        let store = SqliteStore::open_in_memory().unwrap();
        let author = NodeIdentity::generate();
        enrol(&store, "peer-a", &author);
        let stranger = NodeIdentity::generate();

        let good = sign_all(&author, &[name_op("ok", 100, "peer-a")]).unwrap();
        // A forgery at a much HIGHER clock: if it counted, the cursor would jump
        // past the honest op at 900 that has not arrived yet.
        let bad = sign_all(&stranger, &[name_op("forged", 900, "peer-a")]).unwrap();
        let mut batch = good;
        batch.extend(bad);

        let high = accepted_high_water(&store, &batch).unwrap().unwrap();
        assert_eq!(
            high.wall_ms, 100,
            "the forgery must not raise the watermark"
        );
    }

    #[test]
    fn the_node_identity_is_minted_once_and_then_stable() {
        let store = SqliteStore::open_in_memory().unwrap();
        let a = node_identity(&store).unwrap();
        let b = node_identity(&store).unwrap();
        assert_eq!(a.public_hex(), b.public_hex());
    }
}
