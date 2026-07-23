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

/// Hybrid logical clock. Total order is `(wall_ms, counter, peer)`; the field
/// declaration order below is load-bearing because `Ord` is derived.
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
    /// A clock reading from wall time for `peer`, with an explicit counter to
    /// disambiguate two ops minted inside the same millisecond.
    pub fn now(peer: PeerId, counter: u32) -> Self {
        Hlc {
            wall_ms: now_wall_ms(),
            counter,
            peer,
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
