//! String newtype IDs (UUIDv7) and the hybrid logical clock.
//!
//! Every ID is `serde(transparent)` so it serializes as a bare JSON string.
//! `X::new()` mints a fresh time-ordered UUIDv7.

use serde::{Deserialize, Serialize};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

macro_rules! id_type {
    ($(#[$doc:meta])* $name:ident) => {
        $(#[$doc])*
        #[derive(
            Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize,
        )]
        #[serde(transparent)]
        pub struct $name(pub String);

        impl $name {
            /// Mint a fresh time-ordered UUIDv7 identity.
            pub fn new() -> Self {
                $name(uuid::Uuid::now_v7().to_string())
            }
        }

        impl Default for $name {
            fn default() -> Self {
                $name::new()
            }
        }

        impl std::fmt::Display for $name {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str(&self.0)
            }
        }

        impl From<String> for $name {
            fn from(s: String) -> Self {
                $name(s)
            }
        }

        impl From<&str> for $name {
            fn from(s: &str) -> Self {
                $name(s.to_string())
            }
        }

        impl AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                &self.0
            }
        }
    };
}

id_type!(
    /// Identity of a registered repository.
    RepoId
);
id_type!(
    /// Identity of a merged contributor (may span several emails/logins).
    ContributorId
);
id_type!(
    /// Identity of a saved working set (the sharable CRDT unit).
    ContextId
);
id_type!(
    /// Identity of a category (stable across renames of its dotted key).
    CategoryId
);
id_type!(
    /// Identity of a forge/git work item fed to the classifier.
    WorkItemId
);
id_type!(
    /// Identity of a sync peer (this node or a remote one).
    PeerId
);
id_type!(
    /// Identity of one logged agent run (the agent-native write path).
    AgentRunId
);

/// How far ahead of local wall time a remote clock may be and still be folded
/// forward by [`Hlc::observe`]: ±120 s, the same skew bound the shared DMTAP
/// sync engine applies at ingest. Beyond it the remote reading is treated as
/// skew (or hostility) rather than as time that has actually passed.
pub const HLC_SKEW_MS: u64 = 120_000;

/// Hybrid logical clock. The total order is lexicographic by
/// `(wall_ms, counter, peer)` — wall time, then the logical counter, then the
/// identity of the node that emitted it. That is the suite's rule (envoir's
/// `dmtap-sync` orders by `(wall, counter, author)`; FlowStock's
/// `store/hlc.go` by `(wall, counter, node)`), so the engines pick the *same*
/// winner out of the same history rather than merely each converging on one.
///
/// Because `peer` is unique per node, two distinct nodes can never tie, so the
/// order is total across replicas. `Ord` is written out below rather than
/// derived: the order is normative and must not change if a field is ever
/// reordered or added.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Hlc {
    pub wall_ms: u64,
    pub counter: u32,
    pub peer: PeerId,
}

impl PartialOrd for Hlc {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for Hlc {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        self.wall_ms
            .cmp(&other.wall_ms)
            .then(self.counter.cmp(&other.counter))
            .then(self.peer.0.cmp(&other.peer.0))
    }
}

impl Hlc {
    /// Fold a remote clock forward into this one — the HLC *receive* rule. It
    /// is what makes the next clock this node mints sort after every op it has
    /// already seen: without it a node whose wall clock trails a peer's mints a
    /// *lower* clock for the edit that causally follows that peer's op, and LWW
    /// then keeps the older write.
    ///
    /// `peer` is deliberately untouched — the folded reading is still this
    /// node's own clock, so it still breaks ties on this node's identity.
    ///
    /// Callers bound how far ahead `remote` may be (see [`HLC_SKEW_MS`]); this
    /// is only the fold.
    pub fn observe(&mut self, remote: &Hlc) {
        if remote.wall_ms > self.wall_ms {
            self.wall_ms = remote.wall_ms;
            self.counter = remote.counter;
        } else if remote.wall_ms == self.wall_ms && remote.counter > self.counter {
            self.counter = remote.counter;
        }
    }

    /// Serialize to a compact JSON string (used for the DB TEXT columns).
    pub fn encode(&self) -> String {
        serde_json::to_string(self).expect("Hlc serializes")
    }

    /// Parse from the compact JSON string form.
    pub fn decode(s: &str) -> crate::Result<Self> {
        Ok(serde_json::from_str(s)?)
    }
}

/// Milliseconds since the Unix epoch, saturating to 0 before 1970.
pub fn now_wall_ms() -> u64 {
    let nanos = OffsetDateTime::now_utc().unix_timestamp_nanos();
    if nanos <= 0 {
        0
    } else {
        (nanos / 1_000_000) as u64
    }
}

/// Current UTC instant as an RFC3339 string — the canonical timestamp form.
pub fn now_rfc3339() -> String {
    OffsetDateTime::now_utc()
        .format(&Rfc3339)
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cmp::Ordering;

    fn clock(wall_ms: u64, counter: u32, peer: &str) -> Hlc {
        Hlc {
            wall_ms,
            counter,
            peer: PeerId::from(peer),
        }
    }

    /// Every ordering of `items`, by repeatedly picking each element as head.
    fn permutations(items: &[Hlc]) -> Vec<Vec<Hlc>> {
        if items.len() <= 1 {
            return vec![items.to_vec()];
        }
        let mut out = Vec::new();
        for i in 0..items.len() {
            let mut rest = items.to_vec();
            let head = rest.remove(i);
            for mut tail in permutations(&rest) {
                tail.insert(0, head.clone());
                out.push(tail);
            }
        }
        out
    }

    /// The suite's rule, spelled out: wall time outranks the counter, the
    /// counter outranks the peer, and the peer identity is the final tiebreak.
    /// The mirror of `dmtap-sync`'s `hlc_total_order_is_wall_counter_author`.
    #[test]
    fn total_order_is_wall_then_counter_then_peer() {
        let a = clock(1, 0, "zzz");
        let b = clock(1, 1, "aaa");
        assert!(b > a, "counter outranks peer");
        let c = clock(2, 0, "aaa");
        assert!(c > b, "wall outranks counter");
        let d = clock(1, 0, "aaa");
        assert!(a > d, "peer is the final tiebreak");
    }

    /// Two nodes handed the same event set in *any* arrival order must reach
    /// the same order. The set is built so each field in turn is the sole
    /// discriminator, including three events that differ *only* by emitting
    /// peer — which is where a tiebreak on anything but node identity (an op
    /// id, an arrival index, a freshly-minted UUIDv7) makes two nodes disagree.
    #[test]
    fn any_two_nodes_order_the_same_event_set_identically() {
        let events = vec![
            clock(10, 0, "peer-a"),
            clock(10, 0, "peer-b"),
            clock(10, 0, "peer-c"),
            clock(10, 1, "peer-a"),
            clock(11, 0, "peer-c"),
            clock(9, 9, "peer-b"),
        ];

        // No two distinct events may tie. A tie is not benign: sorting is
        // stable, so tied events would keep *arrival* order, and two nodes
        // receive them in different orders by definition.
        for (i, a) in events.iter().enumerate() {
            for b in &events[i + 1..] {
                assert_ne!(a.cmp(b), Ordering::Equal, "{a:?} ties with {b:?}");
            }
        }

        let mut canonical = events.clone();
        canonical.sort();

        let perms = permutations(&events);
        assert_eq!(perms.len(), 720, "every ordering of six events");
        for perm in perms {
            // Two nodes fed the same events in opposite orders.
            let mut node_a = perm.clone();
            node_a.sort();
            let mut node_b = perm;
            node_b.reverse();
            node_b.sort();
            assert_eq!(node_a, canonical, "arrival order changed the merge order");
            assert_eq!(node_b, canonical, "arrival order changed the merge order");
        }
    }

    /// `observe` folds a remote reading forward so the next clock this node
    /// mints sorts after every op it has seen — and a remote reading *behind*
    /// ours never drags the clock backwards.
    #[test]
    fn observe_then_mint_sorts_after_every_seen_op() {
        let mut local = clock(10, 0, "local");
        local.observe(&clock(5, 9, "remote"));
        assert_eq!(
            (local.wall_ms, local.counter),
            (10, 0),
            "no going backwards"
        );

        let ahead = clock(50, 3, "remote");
        local.observe(&ahead);
        // What the store mints next from a stalled wall clock: see `next_hlc`.
        let next = clock(local.wall_ms, local.counter + 1, "local");
        assert!(
            next > ahead,
            "{next:?} must sort after the observed {ahead:?}"
        );
    }

    #[test]
    fn observe_keeps_this_nodes_own_identity() {
        let mut local = clock(1, 0, "local");
        local.observe(&clock(9, 0, "remote"));
        assert_eq!(local.peer, PeerId::from("local"));
        assert_eq!(local.wall_ms, 9);
    }

    #[test]
    fn encode_decode_roundtrips() {
        let h = clock(1_700_000_000_000, 3, "peer-a");
        assert_eq!(Hlc::decode(&h.encode()).unwrap(), h);
    }
}
