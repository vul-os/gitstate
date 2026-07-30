//! Sync identity, individually-signed ops, and the manually-enrolled peer list.
//!
//! A gitstate node that syncs is reachable on the open internet — the whole
//! point of the cloud-node deployment path — so this module is built on three
//! rules, each of which fails closed.
//!
//! 1. **The op is the unit of authenticity.** Every [`SyncOp`] that crosses the
//!    network is wrapped in a [`SignedOp`] carrying an ed25519 signature over a
//!    domain-separated canonical preimage of that one op. A replicated change is
//!    verified *on its own*: nothing is ever trusted merely because it arrived
//!    over a connection that authenticated. That matters because ops are
//!    relayed — a peer re-exports what it received from a third node, so
//!    "the connection was authentic" says nothing about who authored the change.
//!
//! 2. **The clock's tiebreak identity is bound to the signer.** An [`Hlc`]'s
//!    `peer` field is the final tiebreak of the total order, so a node that
//!    could stamp ops with *another* node's peer id could steer every
//!    last-writer-wins decision on that node's behalf. [`SignedOp::verify_from`]
//!    therefore checks the op's clock identity against the enrolled peer whose
//!    key signed it, and refuses on a mismatch.
//!
//! 3. **Discovery is manual and there is no default endpoint.** A peer exists
//!    because an operator typed its URL and its public key ([`SyncPeer`]). There
//!    is no directory, no bootstrap list, no LAN assumption and no fallback: an
//!    empty peer list means this node syncs with nobody, which is the correct
//!    behaviour and not a degraded one.
//!
//! Nothing here opens a socket or touches the filesystem — this crate performs
//! no I/O. The transport that uses these types lives in `gitstate-sync`, and the
//! peer rows live in `gitstate-store`.

use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};

use crate::domain::SyncOp;
use crate::ids::{Hlc, PeerId, HLC_SKEW_MS};
use crate::{Error, Result};

/// Domain-separation tag for a per-op signature preimage. Prefixed to the
/// canonical op bytes so a signature over an op can never be replayed as a
/// signature over anything else gitstate signs (the taxonomy document, a request
/// token). The trailing NUL terminates the tag unambiguously.
pub const DS_OP: &[u8] = b"gitstate-sync-v1/op\0";

/// Domain-separation tag for a request-authentication token (see
/// [`request_token_preimage`]).
pub const DS_REQUEST: &[u8] = b"gitstate-sync-v1/request\0";

/// Domain-separation tag for a response-body signature — the half of mutual
/// authentication that proves to the *caller* it reached the node it enrolled
/// rather than something in the middle.
pub const DS_RESPONSE: &[u8] = b"gitstate-sync-v1/response\0";

/// How far a request token's timestamp may sit from the verifier's clock and
/// still be accepted, in milliseconds. The same ±120 s bound the HLC uses; it
/// bounds the window in which a captured token could be replayed, which is then
/// closed completely by the verifier's seen-token cache.
pub const REQUEST_SKEW_MS: u64 = HLC_SKEW_MS;

// ─────────────────────────── node identity ───────────────────────────

/// This node's sync keypair. Generated on the node, never provisioned: there is
/// no escrow, no default key and no key server.
pub struct NodeIdentity {
    signing: SigningKey,
}

impl NodeIdentity {
    /// Mint a fresh identity from the operating system's CSPRNG.
    pub fn generate() -> Self {
        let mut rng = rand_core::OsRng;
        NodeIdentity {
            signing: SigningKey::generate(&mut rng),
        }
    }

    /// Load from the hex-encoded 32-byte secret scalar seed.
    pub fn from_secret_hex(hex_str: &str) -> Result<Self> {
        let raw = hex::decode(hex_str.trim())
            .map_err(|_| Error::invalid("sync secret key is not valid hex"))?;
        let seed: [u8; 32] = raw
            .try_into()
            .map_err(|_| Error::invalid("sync secret key is not 32 bytes"))?;
        Ok(NodeIdentity {
            signing: SigningKey::from_bytes(&seed),
        })
    }

    /// The hex-encoded secret seed. Only ever handed to the local secret store.
    pub fn secret_hex(&self) -> String {
        hex::encode(self.signing.to_bytes())
    }

    /// The hex-encoded public key — the half an operator gives to a peer.
    pub fn public_hex(&self) -> String {
        hex::encode(self.signing.verifying_key().to_bytes())
    }

    /// Sign one op. The signature covers [`DS_OP`] ‖ the op's canonical bytes,
    /// so it authenticates that op and nothing else.
    pub fn sign_op(&self, op: &SyncOp) -> Result<SignedOp> {
        let sig = self.signing.sign(&op_preimage(op)?);
        Ok(SignedOp {
            op: op.clone(),
            author: self.public_hex(),
            sig: hex::encode(sig.to_bytes()),
        })
    }

    /// Sign an arbitrary domain-separated payload (request tokens, response
    /// bodies). The caller supplies the already-built preimage.
    pub fn sign_bytes(&self, preimage: &[u8]) -> String {
        hex::encode(self.signing.sign(preimage).to_bytes())
    }
}

/// The exact bytes a [`SignedOp`] signature covers: the domain tag followed by
/// the op's canonical JSON. Canonical because `SyncOp` is an externally-tagged
/// enum of scalar fields with a fixed declaration order, so `serde_json` emits
/// one byte sequence for one op — the same property the signed taxonomy relies
/// on.
pub fn op_preimage(op: &SyncOp) -> Result<Vec<u8>> {
    let mut buf = DS_OP.to_vec();
    buf.extend_from_slice(serde_json::to_string(op)?.as_bytes());
    Ok(buf)
}

/// The preimage of a request-authentication token: the tag, the HTTP method, the
/// path, and the caller's millisecond timestamp, NUL-separated so no two
/// distinct triples share a preimage.
pub fn request_token_preimage(method: &str, path: &str, ts_ms: u64) -> Vec<u8> {
    let mut buf = DS_REQUEST.to_vec();
    buf.extend_from_slice(method.as_bytes());
    buf.push(0);
    buf.extend_from_slice(path.as_bytes());
    buf.push(0);
    buf.extend_from_slice(ts_ms.to_string().as_bytes());
    buf
}

/// The preimage of a response-body signature: the tag followed by the exact
/// bytes the caller received.
pub fn response_preimage(body: &[u8]) -> Vec<u8> {
    let mut buf = DS_RESPONSE.to_vec();
    buf.extend_from_slice(body);
    buf
}

/// Verify a hex signature over `preimage` under a hex public key. Every failure
/// mode — bad hex, wrong length, bad signature — is one refusal, so a caller
/// cannot accidentally treat "malformed" as anything but "rejected".
pub fn verify_hex_sig(pubkey_hex: &str, sig_hex: &str, preimage: &[u8]) -> Result<()> {
    let vk_raw = hex::decode(pubkey_hex.trim())
        .map_err(|_| Error::Unauthenticated("public key is not valid hex".into()))?;
    let vk_arr: [u8; 32] = vk_raw
        .try_into()
        .map_err(|_| Error::Unauthenticated("public key is not 32 bytes".into()))?;
    let vk = VerifyingKey::from_bytes(&vk_arr)
        .map_err(|_| Error::Unauthenticated("public key is not on the curve".into()))?;
    let sig_raw = hex::decode(sig_hex.trim())
        .map_err(|_| Error::Unauthenticated("signature is not valid hex".into()))?;
    let sig_arr: [u8; 64] = sig_raw
        .try_into()
        .map_err(|_| Error::Unauthenticated("signature is not 64 bytes".into()))?;
    vk.verify(preimage, &Signature::from_bytes(&sig_arr))
        .map_err(|_| Error::Unauthenticated("signature check failed".into()))
}

// ─────────────────────────── signed ops ───────────────────────────

/// One op with its author's signature over that op alone.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SignedOp {
    pub op: SyncOp,
    /// Hex ed25519 public key of the author.
    pub author: String,
    /// Hex ed25519 signature over [`op_preimage`].
    pub sig: String,
}

impl SignedOp {
    /// Check the signature alone. Says the op is intact and was authored by
    /// whoever holds `author`'s secret — it does **not** say that author is
    /// anybody this node has agreed to accept. Use [`SignedOp::verify_from`] on
    /// an ingest path.
    pub fn verify_signature(&self) -> Result<()> {
        verify_hex_sig(&self.author, &self.sig, &op_preimage(&self.op)?)
    }

    /// The full ingest check against one enrolled peer, in the order a
    /// fail-closed path needs:
    ///
    /// 1. the signature is valid under `peer.pubkey` (not merely under the key
    ///    the message claims — a self-asserted `author` proves nothing);
    /// 2. the op's clock carries that peer's id, so an authenticated peer cannot
    ///    mint ops that tiebreak as some *other* node;
    /// 3. the clock is not implausibly far in the future.
    ///
    /// Step 3 is not decoration. Merge is last-writer-wins on the clock, so a
    /// single op stamped at the end of time would win every field forever and no
    /// honest later write could displace it. A far-future clock is refused
    /// rather than recorded.
    pub fn verify_from(&self, peer: &SyncPeer, now_wall_ms: u64) -> Result<()> {
        if !constant_time_eq(self.author.as_bytes(), peer.pubkey.as_bytes()) {
            return Err(Error::Unauthenticated(
                "op author is not the enrolled peer's key".into(),
            ));
        }
        verify_hex_sig(&peer.pubkey, &self.sig, &op_preimage(&self.op)?)?;
        let hlc = self.op.hlc();
        if hlc.peer != peer.id {
            return Err(Error::Unauthenticated(format!(
                "op clock identity {} does not match the signing peer {}",
                hlc.peer.0, peer.id.0
            )));
        }
        if hlc.wall_ms > now_wall_ms.saturating_add(HLC_SKEW_MS) {
            return Err(Error::Unauthenticated(format!(
                "op clock is {} ms in the future, beyond the {} ms skew bound",
                hlc.wall_ms.saturating_sub(now_wall_ms),
                HLC_SKEW_MS
            )));
        }
        Ok(())
    }
}

/// Length-independent byte comparison for the hex key strings compared above.
/// Not a secret-dependent path (both sides are public keys), but it costs
/// nothing and keeps the habit.
fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

// ─────────────────────────── the peer list ───────────────────────────

/// A peer an operator enrolled by hand: its base URL and its public key, both
/// supplied out of band. There is no field for "discovered how" because there is
/// only one way.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SyncPeer {
    /// The peer's id — the identity its `Hlc`s tiebreak on.
    pub id: PeerId,
    /// Operator-supplied base URL, e.g. `https://gitstate.example.org`. No
    /// default and no scheme guessing: see [`normalize_peer_url`].
    pub url: String,
    /// Hex ed25519 public key, supplied out of band with the URL.
    pub pubkey: String,
    /// Optional operator label, for humans.
    pub label: Option<String>,
    pub added_at: String,
    /// High-water clock of what this node has already pulled from that peer.
    pub last_pull_hlc: Option<Hlc>,
}

// ─────────────────────── the peer wire ───────────────────────

/// `GET /api/sync/pull` — what a node hands a peer that asked for everything
/// after `since`. Every op is individually signed, so the caller re-verifies each
/// one rather than trusting the connection it arrived on.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncPullResp {
    /// The responder's own peer id, so the caller can tell which node answered.
    pub peer_id: PeerId,
    pub ops: Vec<SignedOp>,
}

/// `POST /api/sync/push` — ops a peer offers this node.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SyncPushReq {
    pub ops: Vec<SignedOp>,
}

/// The outcome of an ingest. `rejected` counts ops that failed verification —
/// it is reported separately from `skipped` (ops that verified but lost their
/// merge) because the two mean completely different things operationally.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub struct SyncIngestResp {
    pub applied: u32,
    pub skipped: u32,
    pub rejected: u32,
}

/// Validate and normalize an operator-supplied peer URL.
///
/// Rejects anything that is not an absolute `http`/`https` URL. `http` is
/// allowed — a private network or an SSH tunnel is a legitimate deployment — but
/// it is the operator's explicit choice, never a silent downgrade from a
/// scheme-less string. The trailing slash is trimmed so callers can join paths.
pub fn normalize_peer_url(raw: &str) -> Result<String> {
    let s = raw.trim();
    if s.is_empty() {
        return Err(Error::invalid("peer url is empty"));
    }
    let rest = if let Some(r) = s.strip_prefix("https://") {
        r
    } else if let Some(r) = s.strip_prefix("http://") {
        r
    } else {
        return Err(Error::invalid(format!(
            "peer url must start with http:// or https:// (got {s})"
        )));
    };
    if rest.is_empty() || rest.starts_with('/') {
        return Err(Error::invalid(format!("peer url has no host: {s}")));
    }
    Ok(s.trim_end_matches('/').to_string())
}

/// Validate an operator-supplied hex public key without building a verifier.
pub fn normalize_pubkey(raw: &str) -> Result<String> {
    let s = raw.trim().to_ascii_lowercase();
    let raw_bytes =
        hex::decode(&s).map_err(|_| Error::invalid("peer public key is not valid hex"))?;
    let arr: [u8; 32] = raw_bytes
        .try_into()
        .map_err(|_| Error::invalid("peer public key is not 32 bytes"))?;
    VerifyingKey::from_bytes(&arr)
        .map_err(|_| Error::invalid("peer public key is not a valid ed25519 point"))?;
    Ok(s)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::CtxField;
    use crate::ids::{now_rfc3339, ContextId};

    fn op(peer: &str, wall_ms: u64) -> SyncOp {
        SyncOp::ContextLww {
            id: ContextId::from("c1"),
            field: CtxField::Name,
            value: "hello".into(),
            hlc: Hlc {
                wall_ms,
                counter: 0,
                peer: PeerId::from(peer),
            },
        }
    }

    fn peer_for(id: &str, node: &NodeIdentity) -> SyncPeer {
        SyncPeer {
            id: PeerId::from(id),
            url: "https://peer.example".into(),
            pubkey: node.public_hex(),
            label: None,
            added_at: now_rfc3339(),
            last_pull_hlc: None,
        }
    }

    #[test]
    fn a_signed_op_verifies_and_a_tampered_one_does_not() {
        let node = NodeIdentity::generate();
        let signed = node.sign_op(&op("peer-a", 100)).unwrap();
        signed.verify_signature().unwrap();

        let mut tampered = signed.clone();
        tampered.op = op("peer-a", 101);
        assert!(
            tampered.verify_signature().is_err(),
            "changing the op must invalidate the signature"
        );
    }

    /// The signature is checked against the ENROLLED key, so re-labelling the
    /// `author` field to a key the attacker holds is not a way in.
    #[test]
    fn an_op_signed_by_a_stranger_is_refused() {
        let enrolled = NodeIdentity::generate();
        let stranger = NodeIdentity::generate();
        let peer = peer_for("peer-a", &enrolled);

        let forged = stranger.sign_op(&op("peer-a", 100)).unwrap();
        // Self-consistent: it really is signed by the key it names.
        forged.verify_signature().unwrap();
        // And still refused, because that key is not the one enrolled.
        let err = forged.verify_from(&peer, 1_000).unwrap_err();
        assert!(matches!(err, Error::Unauthenticated(_)), "got {err:?}");
    }

    /// An enrolled peer may not stamp ops with a different node's clock
    /// identity: that identity is the final LWW tiebreak.
    #[test]
    fn an_enrolled_peer_cannot_forge_another_nodes_clock_identity() {
        let node = NodeIdentity::generate();
        let peer = peer_for("peer-a", &node);
        let signed = node.sign_op(&op("peer-b", 100)).unwrap();
        let err = signed.verify_from(&peer, 1_000).unwrap_err();
        assert!(
            format!("{err}").contains("clock identity"),
            "expected a clock-identity refusal, got {err}"
        );
    }

    /// A clock past the skew bound would win every field forever, so it is
    /// refused at the boundary rather than merged and remembered.
    #[test]
    fn a_far_future_clock_is_refused() {
        let node = NodeIdentity::generate();
        let peer = peer_for("peer-a", &node);
        let now = 1_000_000u64;

        let ok = node.sign_op(&op("peer-a", now + HLC_SKEW_MS)).unwrap();
        ok.verify_from(&peer, now).expect("at the bound is fine");

        let bad = node.sign_op(&op("peer-a", now + HLC_SKEW_MS + 1)).unwrap();
        assert!(bad.verify_from(&peer, now).is_err(), "past the bound");

        let end_of_time = node.sign_op(&op("peer-a", u64::MAX)).unwrap();
        assert!(end_of_time.verify_from(&peer, now).is_err());
    }

    #[test]
    fn request_and_response_preimages_are_domain_separated() {
        // The three tags must never collide, or a signature minted for one
        // purpose would verify for another.
        let a = request_token_preimage("GET", "/api/sync/pull", 5);
        let b = response_preimage(b"GET\0/api/sync/pull\0" as &[u8]);
        assert_ne!(a, b);
        assert!(a.starts_with(DS_REQUEST));
        assert!(b.starts_with(DS_RESPONSE));
        assert!(op_preimage(&op("p", 1)).unwrap().starts_with(DS_OP));
    }

    #[test]
    fn a_request_token_verifies_only_for_its_own_method_path_and_time() {
        let node = NodeIdentity::generate();
        let pre = request_token_preimage("GET", "/api/sync/pull", 12_345);
        let sig = node.sign_bytes(&pre);
        verify_hex_sig(&node.public_hex(), &sig, &pre).unwrap();
        for other in [
            request_token_preimage("POST", "/api/sync/pull", 12_345),
            request_token_preimage("GET", "/api/sync/push", 12_345),
            request_token_preimage("GET", "/api/sync/pull", 12_346),
        ] {
            assert!(verify_hex_sig(&node.public_hex(), &sig, &other).is_err());
        }
    }

    #[test]
    fn identity_roundtrips_through_its_hex_secret() {
        let node = NodeIdentity::generate();
        let same = NodeIdentity::from_secret_hex(&node.secret_hex()).unwrap();
        assert_eq!(same.public_hex(), node.public_hex());
        assert!(NodeIdentity::from_secret_hex("nothex").is_err());
        assert!(NodeIdentity::from_secret_hex("aabb").is_err());
    }

    #[test]
    fn peer_urls_must_be_absolute_http_urls() {
        assert_eq!(
            normalize_peer_url("https://gitstate.example.org/").unwrap(),
            "https://gitstate.example.org"
        );
        assert_eq!(
            normalize_peer_url("  http://10.0.0.4:8080  ").unwrap(),
            "http://10.0.0.4:8080"
        );
        for bad in [
            "",
            "gitstate.example.org",
            "ftp://gitstate.example.org",
            "https://",
            "https:///nohost",
        ] {
            assert!(normalize_peer_url(bad).is_err(), "{bad} should be rejected");
        }
    }

    #[test]
    fn peer_pubkeys_must_be_valid_curve_points() {
        let node = NodeIdentity::generate();
        assert_eq!(
            normalize_pubkey(&node.public_hex().to_ascii_uppercase()).unwrap(),
            node.public_hex()
        );
        for bad in ["", "zz", "aabb"] {
            assert!(normalize_pubkey(bad).is_err(), "{bad} should be rejected");
        }
    }
}
