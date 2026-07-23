//! The signed, versioned, content-addressed taxonomy — shipped as *data*, not
//! a running service. A box aligns its category labels to peers by pinning a
//! public key and verifying the signature; it never phones home.
//!
//! Verification is fail-closed: any mismatch yields
//! [`Error::TaxonomyUntrusted`] and the daemon falls back to local-only
//! categories rather than silently trusting an unsigned document.

use crate::{Error, Result};
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

/// Pinned ed25519 public key (hex, 64 chars). A DEV key generated for the
/// transform; the production release process re-signs the default document
/// with the offline release key and replaces this constant. See `decisions.md`.
pub const DEFAULT_TAXONOMY_PUBKEY: &str =
    "3b6a27bcceb6a42d62a3a8d02a6f0d73653215771de243a63ac048a18b59da29";

/// ed25519 signature (hex, 128 chars) over the canonical bytes of the embedded
/// default taxonomy, produced with the private half of the dev key above.
const DEFAULT_TAXONOMY_SIG: &str =
    "3fa8bf71a2bb1b953e45ec9c066ee421700a73fe6016752db28356dd332e51d0fe21d499002685dcc78cb9e2dece5f0d7779b8093a7a5b8703507a319e673b09";

/// The fixed metadata of the embedded default document.
const DEFAULT_SCHEMA: &str = "gitstate.taxonomy/v1";
const DEFAULT_VERSION: &str = "1.0.0";
const DEFAULT_ISSUED_AT: &str = "2026-07-23T00:00:00Z";

/// One category in a [`Taxonomy`]. Field order here is load-bearing: it defines
/// the canonical serialization used for signing.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TaxonomyCategory {
    pub key: String,
    pub label: String,
    pub parent: Option<String>,
    pub color: Option<String>,
    pub description: String,
}

/// A signed taxonomy document.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Taxonomy {
    pub schema: String,
    pub version: String,
    /// sha256 hex of `canonical_bytes`.
    pub id: String,
    pub issued_at: String,
    pub categories: Vec<TaxonomyCategory>,
    /// ed25519 public key, hex(32B).
    pub pubkey: String,
    /// ed25519 signature over `canonical_bytes`, hex(64B).
    pub sig: String,
}

/// The canonical, signature-free projection — the exact bytes that get signed
/// and hashed. Struct field order is the canonical field order.
#[derive(Serialize)]
struct Canonical<'a> {
    schema: &'a str,
    version: &'a str,
    issued_at: &'a str,
    categories: &'a [TaxonomyCategory],
}

impl Taxonomy {
    /// Deterministic serialization of `(schema, version, issued_at,
    /// categories)` as compact UTF-8 JSON, with no signature fields. This is
    /// what gets hashed into `id` and signed into `sig`.
    pub fn canonical_bytes(&self) -> Vec<u8> {
        let c = Canonical {
            schema: &self.schema,
            version: &self.version,
            issued_at: &self.issued_at,
            categories: &self.categories,
        };
        serde_json::to_vec(&c).expect("canonical taxonomy serializes")
    }

    /// The content address of this document: sha256(canonical_bytes), hex.
    pub fn content_id(&self) -> String {
        let mut h = Sha256::new();
        h.update(self.canonical_bytes());
        hex::encode(h.finalize())
    }

    /// Verify the document against a pinned public key:
    /// 1. `id` equals `sha256(canonical_bytes)`,
    /// 2. `pubkey` equals the pinned key,
    /// 3. `sig` verifies over `canonical_bytes`.
    pub fn verify(&self, pinned_pubkey_hex: &str) -> Result<()> {
        let recomputed = self.content_id();
        if recomputed != self.id {
            return Err(Error::TaxonomyUntrusted(format!(
                "content id mismatch: computed {recomputed}, doc claims {}",
                self.id
            )));
        }
        if !self.pubkey.eq_ignore_ascii_case(pinned_pubkey_hex) {
            return Err(Error::TaxonomyUntrusted(
                "public key does not match the pinned key".into(),
            ));
        }

        let pk_bytes = hex::decode(&self.pubkey)
            .map_err(|e| Error::TaxonomyUntrusted(format!("bad pubkey hex: {e}")))?;
        let pk_arr: [u8; 32] = pk_bytes
            .as_slice()
            .try_into()
            .map_err(|_| Error::TaxonomyUntrusted("pubkey is not 32 bytes".into()))?;
        let vk = VerifyingKey::from_bytes(&pk_arr)
            .map_err(|e| Error::TaxonomyUntrusted(format!("invalid pubkey: {e}")))?;

        let sig_bytes = hex::decode(&self.sig)
            .map_err(|e| Error::TaxonomyUntrusted(format!("bad sig hex: {e}")))?;
        let sig_arr: [u8; 64] = sig_bytes
            .as_slice()
            .try_into()
            .map_err(|_| Error::TaxonomyUntrusted("signature is not 64 bytes".into()))?;
        let sig = Signature::from_bytes(&sig_arr);

        vk.verify(&self.canonical_bytes(), &sig)
            .map_err(|e| Error::TaxonomyUntrusted(format!("signature check failed: {e}")))
    }

    /// The embedded, signed default taxonomy (the starter tree from §6). Its
    /// `id` is computed here; `pubkey`/`sig` are the compiled-in dev values.
    pub fn default_taxonomy() -> Taxonomy {
        let categories = default_categories();
        let mut tx = Taxonomy {
            schema: DEFAULT_SCHEMA.to_string(),
            version: DEFAULT_VERSION.to_string(),
            id: String::new(),
            issued_at: DEFAULT_ISSUED_AT.to_string(),
            categories,
            pubkey: DEFAULT_TAXONOMY_PUBKEY.to_string(),
            sig: DEFAULT_TAXONOMY_SIG.to_string(),
        };
        tx.id = tx.content_id();
        tx
    }
}

fn cat(
    key: &str,
    label: &str,
    parent: Option<&str>,
    color: &str,
    description: &str,
) -> TaxonomyCategory {
    TaxonomyCategory {
        key: key.to_string(),
        label: label.to_string(),
        parent: parent.map(|s| s.to_string()),
        color: Some(color.to_string()),
        description: description.to_string(),
    }
}

/// The starter category tree for software work (§6). Keys are stable; labels
/// and colors are advisory.
pub fn default_categories() -> Vec<TaxonomyCategory> {
    vec![
        cat(
            "feature",
            "Feature",
            None,
            "#4f46e5",
            "New capability or user-facing addition",
        ),
        cat(
            "feature.api",
            "API / interface",
            Some("feature"),
            "#6366f1",
            "New or changed API/interface surface",
        ),
        cat(
            "feature.ui",
            "UI / frontend",
            Some("feature"),
            "#818cf8",
            "New or changed UI/frontend surface",
        ),
        cat(
            "feature.data",
            "Data / storage",
            Some("feature"),
            "#a5b4fc",
            "New or changed data/storage surface",
        ),
        cat(
            "feature-flag",
            "Feature flag / rollout",
            None,
            "#0ea5e9",
            "Flag-gated or staged rollout change",
        ),
        cat(
            "bugfix",
            "Bug fix",
            None,
            "#ef4444",
            "Corrects incorrect behaviour",
        ),
        cat(
            "hotfix",
            "Hotfix / incident",
            None,
            "#dc2626",
            "Urgent production/incident fix",
        ),
        cat(
            "refactor",
            "Refactor",
            None,
            "#f59e0b",
            "Behaviour-preserving restructuring",
        ),
        cat(
            "perf",
            "Performance",
            None,
            "#f97316",
            "Latency, throughput, or resource-use improvement",
        ),
        cat(
            "security",
            "Security",
            None,
            "#b91c1c",
            "Vulnerability fix or hardening",
        ),
        cat("test", "Tests", None, "#22c55e", "Adds or changes tests"),
        cat(
            "docs",
            "Documentation",
            None,
            "#14b8a6",
            "Documentation-only change",
        ),
        cat(
            "build",
            "Build / packaging",
            None,
            "#64748b",
            "Build system or packaging change",
        ),
        cat(
            "ci",
            "CI / pipeline",
            None,
            "#475569",
            "Continuous-integration/pipeline change",
        ),
        cat(
            "deps",
            "Dependencies",
            None,
            "#8b5cf6",
            "Dependency add/remove/bump",
        ),
        cat(
            "config",
            "Configuration",
            None,
            "#94a3b8",
            "Configuration or settings change",
        ),
        cat(
            "chore",
            "Chore / housekeeping",
            None,
            "#a8a29e",
            "Routine maintenance with no product impact",
        ),
        cat(
            "revert",
            "Revert",
            None,
            "#e11d48",
            "Reverts a previous change",
        ),
        cat(
            "release",
            "Release / versioning",
            None,
            "#10b981",
            "Version bump or release cut",
        ),
        cat(
            "infra",
            "Infrastructure / ops",
            None,
            "#0891b2",
            "Infrastructure or operational change",
        ),
        cat(
            "agent",
            "Agent-authored change",
            None,
            "#d946ef",
            "Autonomous/agent-authored work (agent-native)",
        ),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn canonical_bytes_are_stable() {
        let tx = Taxonomy::default_taxonomy();
        let a = tx.canonical_bytes();
        let b = tx.canonical_bytes();
        assert_eq!(a, b);
        // id is the sha256 of exactly those bytes
        assert_eq!(tx.id, tx.content_id());
    }

    #[test]
    fn default_has_21_categories() {
        assert_eq!(default_categories().len(), 21);
    }

    #[test]
    fn default_taxonomy_verifies_against_pinned_key() {
        let tx = Taxonomy::default_taxonomy();
        tx.verify(DEFAULT_TAXONOMY_PUBKEY)
            .expect("signed default verifies");
    }

    #[test]
    fn tampered_taxonomy_is_rejected() {
        let mut tx = Taxonomy::default_taxonomy();
        tx.categories[0].label = "Tampered".into();
        // id no longer matches canonical bytes
        assert!(tx.verify(DEFAULT_TAXONOMY_PUBKEY).is_err());
    }
}
