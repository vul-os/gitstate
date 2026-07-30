//! The peer transport: plain HTTP to an address the operator typed.
//!
//! # Why this and nothing else
//!
//! gitstate's peers are two machines an operator chose to connect. Node discovery
//! is manual: a [`SyncPeer`] row exists because somebody ran
//! `gitstate sync peer add --url … --key …`. There is no directory service, no
//! default endpoint, no bootstrap host, no mDNS and no rendezvous broker in any
//! path — including the failure paths. If the peer list is empty this node syncs
//! with nobody; if a peer's URL does not resolve, that peer is unreachable and
//! the others are unaffected.
//!
//! In particular there is **no NAT-traversal dependency**. A node behind NAT with
//! no port forwarded cannot be *dialled* — that is a plain consequence of IP, and
//! it is why the cloud-node deployment path exists: one side gets a routable
//! address and the other side dials it. Every node can both dial and answer, so
//! one reachable node is enough for a pair, and nothing degrades to broken.
//!
//! # What crosses the wire
//!
//! Only [`SignedOp`]s — context and category ops, each individually signed. The
//! authentication of the *connection* lives in [`super::auth`]; the
//! authentication of each *change* lives in the op signature, and the receiver
//! checks both. Nothing else is exposed on these endpoints: commits, diffs,
//! contributions, classifications and effort estimates are local and have no
//! representation in a `SyncOp` at all.
//!
//! # Transport security
//!
//! `https://` peer URLs get TLS from the operator's reverse proxy or a terminating
//! front end — see `docs/DEPLOYMENT.md`. An `http://` URL is accepted because a
//! private link or an SSH tunnel is a legitimate deployment, and it is honest
//! about what it is: the op signatures and the request tokens still authenticate
//! both ends over cleartext, but cleartext is cleartext, and the contexts
//! themselves are readable in flight. That is the operator's explicit choice,
//! never a silent downgrade — `normalize_peer_url` refuses a URL with no scheme
//! rather than guessing one.

use async_trait::async_trait;

use gitstate_core::{
    now_wall_ms, Error, Hlc, NodeIdentity, Result, SignedOp, SyncIngestResp, SyncPeer,
    SyncPullResp, SyncPushReq,
};

use crate::auth::{mint_token, verify_response, SIG_HEADER};

/// Path of the read endpoint. A constant because it is inside the request
/// signature on both sides — a mismatch here is a refusal, not a 404.
pub const PULL_PATH: &str = "/api/sync/pull";
/// Path of the write endpoint.
pub const PUSH_PATH: &str = "/api/sync/push";

/// How this node reaches a peer. A trait so the engine never depends on a
/// concrete network stack, and so tests can drive it against an in-process
/// daemon.
///
/// This is also the seam a NAT-traversal provider would plug into if one is ever
/// wanted: one more implementation beside [`HttpPeerClient`], with the direct one
/// remaining the default. Removing such a provider must degrade to "not reachable
/// from behind NAT", never to broken.
#[async_trait]
pub trait PeerClient: Send + Sync {
    /// Ask `peer` for every op it holds after `since`. The implementation must
    /// verify the response came from the key enrolled for that peer before
    /// returning anything.
    async fn pull(&self, peer: &SyncPeer, since: Option<&Hlc>) -> Result<Vec<SignedOp>>;

    /// Offer `ops` to `peer`.
    async fn push(&self, peer: &SyncPeer, ops: &[SignedOp]) -> Result<SyncIngestResp>;
}

/// The HTTP implementation — the default, and currently the only one.
pub struct HttpPeerClient {
    http: reqwest::Client,
    identity: NodeIdentity,
}

impl HttpPeerClient {
    /// Build a client that authenticates as `identity`.
    pub fn new(identity: NodeIdentity) -> Result<Self> {
        let http = reqwest::Client::builder()
            // A peer that stops answering must not wedge a sync run.
            .timeout(std::time::Duration::from_secs(30))
            .build()
            .map_err(|e| Error::http(format!("building the sync http client: {e}")))?;
        Ok(HttpPeerClient { http, identity })
    }
}

#[async_trait]
impl PeerClient for HttpPeerClient {
    async fn pull(&self, peer: &SyncPeer, since: Option<&Hlc>) -> Result<Vec<SignedOp>> {
        // `since` rides in the query string, but the request signature covers only
        // method and path. That is deliberate and safe: the cursor cannot make a
        // responder disclose anything it would not have disclosed for
        // `since = None`, so tampering with it can only cost the caller ops it then
        // re-requests. Signing it would make the token cursor-specific and buy
        // nothing.
        let mut url = format!("{}{}", peer.url, PULL_PATH);
        if let Some(h) = since {
            url.push_str("?since=");
            url.push_str(&urlencode(&h.encode()));
        }
        let token = mint_token(&self.identity, "GET", PULL_PATH, now_wall_ms());
        let resp = self
            .http
            .get(&url)
            .header(reqwest::header::AUTHORIZATION, token)
            .send()
            .await
            .map_err(|e| Error::http(format!("pull from {}: {e}", peer.url)))?;
        let (body, sig) = signed_body(resp, &peer.url).await?;
        verify_response(peer, &sig, &body)?;
        let parsed: SyncPullResp = serde_json::from_slice(&body)?;
        // The responder names itself; it must be the peer we dialled, or the
        // per-op clock-identity check would be measuring against the wrong peer.
        if parsed.peer_id != peer.id {
            return Err(Error::Unauthenticated(format!(
                "{} answered as peer {} but is enrolled as {}",
                peer.url, parsed.peer_id.0, peer.id.0
            )));
        }
        Ok(parsed.ops)
    }

    async fn push(&self, peer: &SyncPeer, ops: &[SignedOp]) -> Result<SyncIngestResp> {
        let url = format!("{}{}", peer.url, PUSH_PATH);
        let token = mint_token(&self.identity, "POST", PUSH_PATH, now_wall_ms());
        let resp = self
            .http
            .post(&url)
            .header(reqwest::header::AUTHORIZATION, token)
            .json(&SyncPushReq { ops: ops.to_vec() })
            .send()
            .await
            .map_err(|e| Error::http(format!("push to {}: {e}", peer.url)))?;
        let (body, sig) = signed_body(resp, &peer.url).await?;
        verify_response(peer, &sig, &body)?;
        Ok(serde_json::from_slice(&body)?)
    }
}

/// Read a response's status, body bytes and signature header.
///
/// The signature header is required, not optional: a responder that does not sign
/// has not authenticated itself, and treating a missing header as "nothing to
/// check" would turn the mutual half of the handshake into a suggestion.
async fn signed_body(resp: reqwest::Response, peer_url: &str) -> Result<(Vec<u8>, String)> {
    let status = resp.status();
    let sig = resp
        .headers()
        .get(SIG_HEADER)
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());
    let body = resp
        .bytes()
        .await
        .map_err(|e| Error::http(format!("reading the response from {peer_url}: {e}")))?
        .to_vec();
    if !status.is_success() {
        let detail = String::from_utf8_lossy(&body);
        let msg = format!("{peer_url} answered {status}: {}", detail.trim());
        return Err(if status == reqwest::StatusCode::UNAUTHORIZED {
            Error::Unauthenticated(msg)
        } else {
            Error::http(msg)
        });
    }
    let sig = sig.ok_or_else(|| {
        Error::Unauthenticated(format!("{peer_url} returned no {SIG_HEADER} signature"))
    })?;
    Ok((body, sig))
}

/// Minimal percent-encoding for the one value gitstate puts in a query string
/// (a JSON-encoded `Hlc`). Written out rather than pulled in, because the whole
/// alphabet involved is ASCII and a dependency for this would be silly.
fn urlencode(s: &str) -> String {
    let mut out = String::with_capacity(s.len() * 3);
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{now_rfc3339, PeerId};

    fn peer(url: &str) -> SyncPeer {
        SyncPeer {
            id: PeerId::from("peer-a"),
            url: url.into(),
            pubkey: NodeIdentity::generate().public_hex(),
            label: None,
            added_at: now_rfc3339(),
            last_pull_hlc: None,
        }
    }

    #[test]
    fn the_cursor_is_percent_encoded_into_the_query() {
        let h = Hlc {
            wall_ms: 7,
            counter: 1,
            peer: PeerId::from("peer-a"),
        };
        let enc = urlencode(&h.encode());
        assert!(!enc.contains('{'), "{enc}");
        assert!(!enc.contains('"'), "{enc}");
        assert!(enc.contains("%7B"), "{enc}");
    }

    /// An unreachable peer is an error for that peer, not a panic and not a silent
    /// success. Port 1 on the loopback refuses immediately, so this makes no
    /// outbound connection to anywhere real.
    #[tokio::test]
    async fn an_unreachable_peer_reports_an_error() {
        let client = HttpPeerClient::new(NodeIdentity::generate()).unwrap();
        let err = client
            .pull(&peer("http://127.0.0.1:1"), None)
            .await
            .expect_err("nothing is listening on port 1");
        assert!(matches!(err, Error::Http(_)), "{err:?}");
    }
}
