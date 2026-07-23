//! Thin loader over [`gitstate_core::Taxonomy::verify`]. Resolves the pinned
//! public key (env `GITSTATE_TAXONOMY_PUBKEY`, else the compiled-in default),
//! then verifies. Fail-closed: an untrusted document is an error, never a
//! silently-trusted fallback.

use gitstate_core::{Result, Taxonomy, DEFAULT_TAXONOMY_PUBKEY};

/// Resolve the pinned taxonomy public key.
pub fn pinned_pubkey() -> String {
    std::env::var("GITSTATE_TAXONOMY_PUBKEY")
        .ok()
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| DEFAULT_TAXONOMY_PUBKEY.to_string())
}

/// Load and verify a taxonomy. `None` yields the embedded, signed default;
/// `Some(bytes)` parses a runtime override (e.g. from
/// `GITSTATE_TAXONOMY_PATH`). Either way the signature is checked against the
/// pinned key before the document is returned.
pub fn load_and_verify_taxonomy(bytes: Option<&[u8]>) -> Result<Taxonomy> {
    let pinned = pinned_pubkey();
    let tx = match bytes {
        None => Taxonomy::default_taxonomy(),
        Some(b) => serde_json::from_slice::<Taxonomy>(b)?,
    };
    tx.verify(&pinned)?;
    Ok(tx)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn embedded_default_loads_and_verifies() {
        let tx = load_and_verify_taxonomy(None).expect("default verifies");
        assert_eq!(tx.categories.len(), 21);
    }
}
