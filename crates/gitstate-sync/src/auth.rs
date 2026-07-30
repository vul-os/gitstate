//! Connection authentication for the peer endpoints, and the replay guard that
//! makes it single-use.
//!
//! Per-op signatures (`gitstate_core::peer`) answer "who authored this change".
//! They do not answer "may this caller talk to me at all", and on a cloud node
//! that second question has to be answered before any context leaves the box —
//! `GET /api/sync/pull` hands over every context and category this node holds.
//!
//! So each request carries a token the caller signs with its node key:
//!
//! ```text
//! Authorization: GitState-Sync <pubkey-hex>.<unix-ms>.<sig-hex>
//! ```
//!
//! The signature covers the domain tag, the HTTP method, the path and the
//! timestamp ([`gitstate_core::request_token_preimage`]), so a token minted for a
//! read cannot be replayed as a write, or against a different path.
//!
//! Two independent bounds make it single-use. The timestamp must sit within
//! ±[`REQUEST_SKEW_MS`] of the verifier's clock, which bounds how long a captured
//! token could be worth anything; and inside that window [`ReplayGuard`] refuses
//! any signature it has already accepted, which closes it entirely. Either bound
//! alone leaves a hole — the clock bound alone gives a 4-minute replay window,
//! and the guard alone would have to remember every token ever seen.
//!
//! The other half of mutual authentication runs in the opposite direction: the
//! responder signs the exact response body it returns, and the caller checks that
//! signature against the public key it enrolled for that peer. A caller therefore
//! never accepts ops from something that merely answered on the peer's URL.

use std::collections::HashMap;
use std::sync::Mutex;

use gitstate_core::{
    now_wall_ms, request_token_preimage, response_preimage, verify_hex_sig, Error, NodeIdentity,
    Result, Store, SyncPeer, REQUEST_SKEW_MS,
};

/// The `Authorization` scheme token.
pub const AUTH_SCHEME: &str = "GitState-Sync";
/// Response header carrying the responder's signature over the body bytes.
pub const SIG_HEADER: &str = "x-gitstate-sync-sig";
/// Response header carrying the responder's peer id (informational; the key is
/// what authenticates).
pub const PEER_HEADER: &str = "x-gitstate-sync-peer";

/// Build the `Authorization` header value for one request.
pub fn mint_token(identity: &NodeIdentity, method: &str, path: &str, ts_ms: u64) -> String {
    let sig = identity.sign_bytes(&request_token_preimage(method, path, ts_ms));
    format!("{AUTH_SCHEME} {}.{}.{}", identity.public_hex(), ts_ms, sig)
}

/// The three parts of a parsed token.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Token {
    pub pubkey: String,
    pub ts_ms: u64,
    pub sig: String,
}

/// Parse an `Authorization` value. Every malformed shape is one refusal.
pub fn parse_token(header: &str) -> Result<Token> {
    let rest = header
        .strip_prefix(AUTH_SCHEME)
        .and_then(|r| r.strip_prefix(' '))
        .ok_or_else(|| {
            Error::Unauthenticated("authorization scheme is not GitState-Sync".into())
        })?;
    let parts: Vec<&str> = rest.trim().split('.').collect();
    if parts.len() != 3 {
        return Err(Error::Unauthenticated(
            "authorization token is not <pubkey>.<ts>.<sig>".into(),
        ));
    }
    let ts_ms: u64 = parts[1]
        .parse()
        .map_err(|_| Error::Unauthenticated("authorization timestamp is not a number".into()))?;
    Ok(Token {
        pubkey: parts[0].trim().to_ascii_lowercase(),
        ts_ms,
        sig: parts[2].trim().to_string(),
    })
}

/// Remembers accepted token signatures for as long as one could still be inside
/// the clock window, so a captured token cannot be used twice.
///
/// Bounded by time, not by count: entries expire, and the window is
/// `2 × REQUEST_SKEW_MS` wide because a token may legitimately arrive that far
/// on either side of local time.
#[derive(Debug, Default)]
pub struct ReplayGuard {
    seen: Mutex<HashMap<String, u64>>,
}

impl ReplayGuard {
    pub fn new() -> Self {
        Self::default()
    }

    /// Accept `sig` once. A second presentation inside the window is refused.
    pub fn accept_once(&self, sig: &str, now_ms: u64) -> Result<()> {
        let horizon = now_ms.saturating_sub(REQUEST_SKEW_MS.saturating_mul(2));
        let mut seen = self.seen.lock().unwrap();
        seen.retain(|_, ts| *ts >= horizon);
        if seen.contains_key(sig) {
            return Err(Error::Unauthenticated(
                "authorization token has already been used".into(),
            ));
        }
        seen.insert(sig.to_string(), now_ms);
        Ok(())
    }

    /// How many tokens are currently remembered. For tests and diagnostics.
    pub fn remembered(&self) -> usize {
        self.seen.lock().unwrap().len()
    }
}

/// Authenticate a request and return the enrolled peer it came from.
///
/// Fail-closed, in this order: a parseable token, a *known* key, a fresh
/// timestamp, a valid signature, and a signature not seen before. The key lookup
/// comes before the cryptography so an unknown caller cannot make this node do
/// signature work, but note the consequence is only that: the refusal is the same
/// `Unauthenticated` either way, and the reason is never sent back.
pub fn authenticate(
    store: &dyn Store,
    guard: &ReplayGuard,
    header: Option<&str>,
    method: &str,
    path: &str,
) -> Result<SyncPeer> {
    let header = header.ok_or_else(|| {
        Error::Unauthenticated("no authorization header on a sync request".into())
    })?;
    let token = parse_token(header)?;

    let peer = store
        .sync_peer_by_pubkey(&token.pubkey)?
        .ok_or_else(|| Error::Unauthenticated("caller's key is not an enrolled peer".into()))?;

    let now = now_wall_ms();
    let drift = now.abs_diff(token.ts_ms);
    if drift > REQUEST_SKEW_MS {
        return Err(Error::Unauthenticated(format!(
            "authorization timestamp is {drift} ms from local time, beyond the {REQUEST_SKEW_MS} ms window"
        )));
    }

    verify_hex_sig(
        &peer.pubkey,
        &token.sig,
        &request_token_preimage(method, path, token.ts_ms),
    )?;
    guard.accept_once(&token.sig, now)?;
    Ok(peer)
}

/// Sign a response body so the caller can confirm it reached the node it
/// enrolled.
pub fn sign_response(identity: &NodeIdentity, body: &[u8]) -> String {
    identity.sign_bytes(&response_preimage(body))
}

/// Check a response body against the key the operator enrolled for that peer.
pub fn verify_response(peer: &SyncPeer, sig_hex: &str, body: &[u8]) -> Result<()> {
    verify_hex_sig(&peer.pubkey, sig_hex, &response_preimage(body)).map_err(|_| {
        Error::Unauthenticated(format!(
            "response from {} is not signed by the enrolled key",
            peer.url
        ))
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{now_rfc3339, PeerId};
    use gitstate_store::SqliteStore;

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

    #[test]
    fn a_token_from_an_enrolled_peer_authenticates_once() {
        let store = SqliteStore::open_in_memory().unwrap();
        let caller = NodeIdentity::generate();
        enrol(&store, "peer-a", &caller);
        let guard = ReplayGuard::new();

        let header = mint_token(&caller, "GET", "/api/sync/pull", now_wall_ms());
        let peer = authenticate(&store, &guard, Some(&header), "GET", "/api/sync/pull")
            .expect("first use authenticates");
        assert_eq!(peer.id, PeerId::from("peer-a"));

        let err = authenticate(&store, &guard, Some(&header), "GET", "/api/sync/pull")
            .expect_err("the same token must not work twice");
        assert!(format!("{err}").contains("already been used"), "{err}");
    }

    #[test]
    fn an_unenrolled_caller_is_refused() {
        let store = SqliteStore::open_in_memory().unwrap();
        let stranger = NodeIdentity::generate();
        let guard = ReplayGuard::new();
        let header = mint_token(&stranger, "GET", "/api/sync/pull", now_wall_ms());
        let err = authenticate(&store, &guard, Some(&header), "GET", "/api/sync/pull")
            .expect_err("a stranger's own valid signature is not admission");
        assert!(matches!(err, Error::Unauthenticated(_)), "{err:?}");
    }

    /// The path and method are inside the signature, so a token minted for the
    /// read endpoint cannot be spent on the write endpoint.
    #[test]
    fn a_read_token_cannot_be_spent_on_the_write_endpoint() {
        let store = SqliteStore::open_in_memory().unwrap();
        let caller = NodeIdentity::generate();
        enrol(&store, "peer-a", &caller);
        let guard = ReplayGuard::new();
        let header = mint_token(&caller, "GET", "/api/sync/pull", now_wall_ms());
        assert!(
            authenticate(&store, &guard, Some(&header), "POST", "/api/sync/push").is_err(),
            "method+path must be bound into the token"
        );
    }

    #[test]
    fn a_stale_timestamp_is_refused_and_a_missing_header_too() {
        let store = SqliteStore::open_in_memory().unwrap();
        let caller = NodeIdentity::generate();
        enrol(&store, "peer-a", &caller);
        let guard = ReplayGuard::new();

        let stale = mint_token(
            &caller,
            "GET",
            "/api/sync/pull",
            now_wall_ms().saturating_sub(REQUEST_SKEW_MS + 1_000),
        );
        assert!(authenticate(&store, &guard, Some(&stale), "GET", "/api/sync/pull").is_err());
        assert!(authenticate(&store, &guard, None, "GET", "/api/sync/pull").is_err());
        for bad in ["Bearer abc", "GitState-Sync too.few", "GitState-Sync a.b.c"] {
            assert!(
                authenticate(&store, &guard, Some(bad), "GET", "/api/sync/pull").is_err(),
                "{bad}"
            );
        }
    }

    #[test]
    fn the_replay_guard_forgets_tokens_that_can_no_longer_be_fresh() {
        let guard = ReplayGuard::new();
        guard.accept_once("sig-1", 1_000_000).unwrap();
        assert_eq!(guard.remembered(), 1);
        // Far enough ahead that "sig-1" could no longer pass the clock window.
        guard
            .accept_once("sig-2", 1_000_000 + REQUEST_SKEW_MS * 2 + 1)
            .unwrap();
        assert_eq!(guard.remembered(), 1, "the expired entry is dropped");
        guard
            .accept_once("sig-1", 1_000_000 + REQUEST_SKEW_MS * 2 + 1)
            .expect("and is no longer treated as a replay");
    }

    #[test]
    fn a_response_verifies_only_under_the_enrolled_key() {
        let store = SqliteStore::open_in_memory().unwrap();
        let responder = NodeIdentity::generate();
        let peer = enrol(&store, "peer-a", &responder);
        let body = br#"{"ops":[]}"#;
        let sig = sign_response(&responder, body);
        verify_response(&peer, &sig, body).unwrap();
        assert!(
            verify_response(&peer, &sig, br#"{"ops":[1]}"#).is_err(),
            "a different body must not verify"
        );
        let impostor = NodeIdentity::generate();
        assert!(
            verify_response(&peer, &sign_response(&impostor, body), body).is_err(),
            "a man in the middle answering on the peer's URL must not verify"
        );
    }
}
