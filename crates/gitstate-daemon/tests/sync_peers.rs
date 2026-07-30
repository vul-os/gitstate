//! Two real gitstate nodes, two real sockets, replicating.
//!
//! Everything else about sync is tested against a store or a router in memory.
//! This file is the one that proves the claim an operator actually cares about:
//! stand two daemons up, have each operator enrol the other by URL and key, run a
//! round, and the context created on one node is present on the other.
//!
//! It also proves the refusals, which matter more than the happy path, because
//! the happy path is what gets demonstrated and the refusals are what get assumed:
//!
//! * a caller with no credentials gets 401 from `pull`, and learns nothing;
//! * a caller whose key is not enrolled gets 401 even though its own signature is
//!   perfectly valid;
//! * a captured request token cannot be spent twice;
//! * a node with no peers enrolled reaches nobody and says so.
//!
//! Both nodes bind loopback here, on two distinct ephemeral ports. That exercises
//! the real socket path, real HTTP, real signing and real verification; what it
//! does not exercise is a routable address, which no test can portably bind. The
//! non-loopback behaviour that matters is the bind-time guard, and that has its own
//! tests in `gitstate_daemon::state`.

use std::net::SocketAddr;
use std::sync::Arc;

use gitstate_core::{
    now_rfc3339, Context, ContextId, Hlc, NodeIdentity, PeerId, RepoId, Store, SyncPeer,
};
use gitstate_daemon::{AdminAuth, AppState, Daemon, ForgeRegistry};
use gitstate_store::SqliteStore;
use gitstate_sync::auth::{mint_token, ReplayGuard};
use gitstate_sync::{PeerClient, SyncNode, PULL_PATH};

/// One node: its store, its address, and the identity a peer must enrol.
struct Node {
    store: Arc<SqliteStore>,
    addr: SocketAddr,
    peer_id: PeerId,
    pubkey: String,
}

impl Node {
    fn url(&self) -> String {
        format!("http://{}", self.addr)
    }

    /// The `SyncPeer` row the OTHER node's operator would type in.
    fn as_peer(&self) -> SyncPeer {
        SyncPeer {
            id: self.peer_id.clone(),
            url: self.url(),
            pubkey: self.pubkey.clone(),
            label: None,
            added_at: now_rfc3339(),
            last_pull_hlc: None,
        }
    }
}

async fn start_node() -> Node {
    let store = Arc::new(SqliteStore::open_in_memory().unwrap());
    // Give the node a stable peer id, as a real install has.
    let peer_id = PeerId::new();
    store.kv_set("peer_id", &peer_id.0).unwrap();
    let pubkey = gitstate_sync::node_identity(store.as_ref())
        .unwrap()
        .public_hex();

    let state = AppState {
        store: store.clone() as Arc<dyn Store>,
        forge: ForgeRegistry::from_env(),
        classifier: gitstate_classify::default_classifier().into(),
        taxonomy: Arc::new(gitstate_core::Taxonomy::default_taxonomy()),
        sync: None,
        web_dist: None,
        admin_auth: AdminAuth::LocalOnly,
        replay_guard: Arc::new(ReplayGuard::new()),
    };
    let (addr, _handle) = Daemon::new(state).serve_ephemeral().await.unwrap();
    Node {
        store,
        addr,
        peer_id,
        pubkey,
    }
}

fn make_context(store: &SqliteStore, id: &str, name: &str) {
    store
        .upsert_context(&Context {
            id: ContextId::from(id),
            name: name.into(),
            description: "made on one node".into(),
            repo_ids: vec![RepoId::from("r1")],
            pr_refs: vec![],
            notes: "notes".into(),
            tags: vec!["shared".into()],
            created_at: now_rfc3339(),
            updated_at: now_rfc3339(),
            // Overwritten by the store with a freshly-minted clock.
            hlc: Hlc {
                wall_ms: 0,
                counter: 0,
                peer: PeerId::from(""),
            },
            deleted: false,
        })
        .unwrap();
}

/// The whole point, end to end.
#[tokio::test]
async fn two_nodes_enrolled_by_hand_replicate_a_context_over_a_socket() {
    let a = start_node().await;
    let b = start_node().await;

    // Manual enrolment, in both directions — exactly what the two operators type.
    a.store.upsert_sync_peer(&b.as_peer()).unwrap();
    b.store.upsert_sync_peer(&a.as_peer()).unwrap();

    // A makes a context. B has never heard of it.
    make_context(&a.store, "ctx-1", "Q3 refactor");
    assert!(b
        .store
        .get_context(&ContextId::from("ctx-1"))
        .unwrap()
        .is_none());

    // One round from A: push what it has, pull what B has.
    let node_a = SyncNode::from_store(a.store.clone() as Arc<dyn Store>).unwrap();
    let reports = node_a.sync_all().await.unwrap();
    assert_eq!(reports.len(), 1);
    assert!(
        reports[0].error.is_none(),
        "the round must succeed: {:?}",
        reports[0].error
    );
    assert!(reports[0].pushed > 0, "A had ops to offer");

    // B now holds the context, reconstructed from the ops — not a copy of a row.
    let landed = b
        .store
        .get_context(&ContextId::from("ctx-1"))
        .unwrap()
        .expect("the context replicated to B");
    assert_eq!(landed.name, "Q3 refactor");
    assert_eq!(landed.description, "made on one node");
    assert_eq!(landed.notes, "notes");
    assert_eq!(landed.tags, vec!["shared".to_string()]);
    assert_eq!(landed.repo_ids, vec![RepoId::from("r1")]);

    // And the reverse direction: B edits, A pulls it back.
    make_context(&b.store, "ctx-2", "made on B");
    let again = node_a.sync_all().await.unwrap();
    assert!(again[0].error.is_none(), "{:?}", again[0].error);
    assert!(
        a.store
            .get_context(&ContextId::from("ctx-2"))
            .unwrap()
            .is_some(),
        "the pull direction must work too, so a node behind NAT still converges"
    );
}

/// No credentials, no data. The response must not disclose what was wrong.
#[tokio::test]
async fn pull_without_credentials_is_refused_and_says_nothing() {
    let a = start_node().await;
    let http = reqwest::Client::new();
    let resp = http
        .get(format!("{}{}", a.url(), PULL_PATH))
        .send()
        .await
        .unwrap();
    assert_eq!(resp.status(), reqwest::StatusCode::UNAUTHORIZED);
    let body = resp.text().await.unwrap();
    assert!(
        body.contains("unauthenticated"),
        "expected the bare code, got {body}"
    );
    for leak in ["enrolled", "signature", "timestamp", "authorization header"] {
        assert!(
            !body.contains(leak),
            "the refusal must not explain itself to a stranger: {body}"
        );
    }
}

/// A stranger's own valid signature is not admission. This is the check that
/// makes an exposed node safe: cryptographic self-consistency proves identity, and
/// identity is not authorisation.
#[tokio::test]
async fn a_perfectly_signed_request_from_an_unenrolled_key_is_refused() {
    let a = start_node().await;
    let stranger = NodeIdentity::generate();
    let http = reqwest::Client::new();

    let token = mint_token(&stranger, "GET", PULL_PATH, gitstate_core::now_wall_ms());
    let resp = http
        .get(format!("{}{}", a.url(), PULL_PATH))
        .header(reqwest::header::AUTHORIZATION, token)
        .send()
        .await
        .unwrap();
    assert_eq!(resp.status(), reqwest::StatusCode::UNAUTHORIZED);
}

/// A captured token is worth one use. Replaying it is refused even though every
/// other check on it still passes.
#[tokio::test]
async fn a_captured_request_token_cannot_be_replayed() {
    let a = start_node().await;
    let b = start_node().await;
    a.store.upsert_sync_peer(&b.as_peer()).unwrap();

    // B's identity is what A enrolled, so this token is entirely legitimate.
    let b_identity = gitstate_sync::node_identity(b.store.as_ref()).unwrap();
    let token = mint_token(&b_identity, "GET", PULL_PATH, gitstate_core::now_wall_ms());

    let http = reqwest::Client::new();
    let first = http
        .get(format!("{}{}", a.url(), PULL_PATH))
        .header(reqwest::header::AUTHORIZATION, token.clone())
        .send()
        .await
        .unwrap();
    assert_eq!(first.status(), reqwest::StatusCode::OK, "first use is fine");
    // The responder signs its body, so the caller can tell which node answered.
    assert!(
        first
            .headers()
            .get(gitstate_sync::auth::SIG_HEADER)
            .is_some(),
        "every peer response must be signed"
    );

    let second = http
        .get(format!("{}{}", a.url(), PULL_PATH))
        .header(reqwest::header::AUTHORIZATION, token)
        .send()
        .await
        .unwrap();
    assert_eq!(
        second.status(),
        reqwest::StatusCode::UNAUTHORIZED,
        "the same token must not work twice"
    );
}

/// A response signed by the wrong key is refused by the caller, so a node that
/// answers on a peer's address without holding its key gets nowhere. This is the
/// mutual half of the authentication.
#[tokio::test]
async fn a_node_answering_on_the_peers_address_without_its_key_is_refused() {
    let a = start_node().await;
    let impostor = start_node().await;
    // A enrols a peer at the impostor's ADDRESS but with a different node's KEY —
    // which is what a hijacked DNS record or a transparent proxy looks like.
    let mut peer = impostor.as_peer();
    peer.pubkey = NodeIdentity::generate().public_hex();
    peer.id = PeerId::from("some-other-node");
    // The impostor must let A in at all, or the test would prove nothing about the
    // response check.
    impostor
        .store
        .upsert_sync_peer(&SyncPeer {
            id: a.peer_id.clone(),
            url: a.url(),
            pubkey: a.pubkey.clone(),
            label: None,
            added_at: now_rfc3339(),
            last_pull_hlc: None,
        })
        .unwrap();
    a.store.upsert_sync_peer(&peer).unwrap();

    let client =
        gitstate_sync::HttpPeerClient::new(gitstate_sync::node_identity(a.store.as_ref()).unwrap())
            .unwrap();
    let err = client
        .pull(&peer, None)
        .await
        .expect_err("a response not signed by the enrolled key must be refused");
    assert!(
        matches!(err, gitstate_core::Error::Unauthenticated(_)),
        "{err:?}"
    );
}

/// With nothing enrolled, a round reaches nobody — and reports that rather than
/// looking like a success.
#[tokio::test]
async fn a_node_with_no_peers_replicates_with_nobody() {
    let a = start_node().await;
    assert!(a.store.list_sync_peers().unwrap().is_empty());
    let node = SyncNode::from_store(a.store.clone() as Arc<dyn Store>).unwrap();
    assert!(
        node.sync_all().await.unwrap().is_empty(),
        "no peers means no rounds, not a default endpoint"
    );
}

/// Removing an enrolment closes the door: that peer's previously-accepted key is
/// no longer admitted, and its ops stop being ingested.
#[tokio::test]
async fn removing_an_enrolment_stops_admitting_that_peers_ops() {
    let a = start_node().await;
    let b = start_node().await;
    a.store.upsert_sync_peer(&b.as_peer()).unwrap();
    b.store.upsert_sync_peer(&a.as_peer()).unwrap();

    make_context(&b.store, "ctx-b", "from B");
    let node_a = SyncNode::from_store(a.store.clone() as Arc<dyn Store>).unwrap();
    node_a.sync_all().await.unwrap();
    assert!(a
        .store
        .get_context(&ContextId::from("ctx-b"))
        .unwrap()
        .is_some());

    // Un-enrol B, then have B change something and offer it.
    assert!(a.store.delete_sync_peer(&b.peer_id).unwrap());
    make_context(&b.store, "ctx-b2", "after removal");
    let signed = b.store.signed_ops_since(None).unwrap();
    let out = gitstate_sync::ingest_signed(a.store.as_ref(), &signed).unwrap();
    assert_eq!(
        out.applied, 0,
        "nothing from an un-enrolled peer is applied"
    );
    assert!(out.rejected > 0, "and the rejection is counted");
    assert!(a
        .store
        .get_context(&ContextId::from("ctx-b2"))
        .unwrap()
        .is_none());
}

/// A late op with a clock BELOW the highest already seen from that peer must still
/// arrive.
///
/// This is the lost-write hazard of using one scalar clock as a pull cursor. B
/// relays ops from two different authors; the second author's clock is far ahead of
/// the first's — which is normal, HLC wall readings are per-node. If A filtered its
/// next pull at "the highest clock I accepted", the first author's *later* op, whose
/// clock is legitimately lower, would be filtered out for good, and nothing would
/// report it. `pull_from` therefore asks for the whole log; this test is what stops
/// somebody reintroducing the cursor as a filter.
#[tokio::test]
async fn an_op_whose_clock_is_below_the_recorded_high_water_still_arrives() {
    let a = start_node().await;
    let b = start_node().await;
    a.store.upsert_sync_peer(&b.as_peer()).unwrap();
    b.store.upsert_sync_peer(&a.as_peer()).unwrap();

    // A third author, enrolled on both, whose wall clock trails B's badly.
    let c_key = NodeIdentity::generate();
    let c_id = PeerId::from("peer-c-slow-clock");
    let c_peer = SyncPeer {
        id: c_id.clone(),
        // C is never actually dialled — only its ENROLMENT matters here, because
        // its ops reach A by being relayed through B. Loopback port 1 so the
        // connection is refused immediately: `sync_all` does try every enrolled
        // peer, and a blackhole address would make each round wait out the client
        // timeout. That C's leg fails while B's succeeds is itself the property
        // that one unreachable peer must not stop replication with the rest.
        url: "http://127.0.0.1:1".into(),
        pubkey: c_key.public_hex(),
        label: Some("slow clock".into()),
        added_at: now_rfc3339(),
        last_pull_hlc: None,
    };
    a.store.upsert_sync_peer(&c_peer).unwrap();
    b.store.upsert_sync_peer(&c_peer).unwrap();

    let c_op = |name: &str, wall_ms: u64| gitstate_core::SyncOp::ContextLww {
        id: ContextId::from("ctx-c"),
        field: gitstate_core::CtxField::Name,
        value: name.into(),
        hlc: Hlc {
            wall_ms,
            counter: 0,
            peer: c_id.clone(),
        },
    };

    // Round 1: B holds C's early op, plus B's own writes at a much higher clock
    // (the store mints those from real wall-clock milliseconds).
    let early = gitstate_sync::sign_all(&c_key, &[c_op("early", 1_000)]).unwrap();
    assert_eq!(
        gitstate_sync::ingest_signed(b.store.as_ref(), &early)
            .unwrap()
            .applied,
        1
    );
    make_context(&b.store, "ctx-b", "from B, high clock");

    let node_a = SyncNode::from_store(a.store.clone() as Arc<dyn Store>).unwrap();
    node_a.sync_all().await.unwrap();
    assert_eq!(
        a.store
            .get_context(&ContextId::from("ctx-c"))
            .unwrap()
            .unwrap()
            .name,
        "early"
    );

    // A's recorded high-water is now B's clock — milliseconds since 1970, i.e.
    // astronomically above 1_000. Confirm that, or the test proves nothing.
    let recorded = a
        .store
        .list_sync_peers()
        .unwrap()
        .into_iter()
        .find(|p| p.id == b.peer_id)
        .unwrap()
        .last_pull_hlc
        .expect("a high-water mark was recorded");
    assert!(
        recorded.wall_ms > 2_000,
        "the recorded mark must be far above C's clock for this to be a real test: {recorded:?}"
    );

    // Round 2: C's NEXT op — later in C's history, but at a clock below A's mark.
    let late = gitstate_sync::sign_all(&c_key, &[c_op("late but higher for C", 1_500)]).unwrap();
    assert_eq!(
        gitstate_sync::ingest_signed(b.store.as_ref(), &late)
            .unwrap()
            .applied,
        1
    );
    node_a.sync_all().await.unwrap();

    assert_eq!(
        a.store
            .get_context(&ContextId::from("ctx-c"))
            .unwrap()
            .unwrap()
            .name,
        "late but higher for C",
        "an op below the recorded high-water mark was dropped — the scalar cursor is being used as a filter again"
    );
}
