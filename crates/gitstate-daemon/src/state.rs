//! Application state, the forge registry, taxonomy/web-dist resolution, and the
//! [`Daemon`] type that binds and serves. [`build_state_from_env`] is the one
//! wiring both the `gitstated` binary and the Tauri shell call.

use std::net::{Ipv4Addr, SocketAddr};
use std::path::PathBuf;
use std::sync::Arc;

use axum::Router;
use tokio::task::JoinHandle;

use gitstate_classify::{default_classifier, load_and_verify_taxonomy};
use gitstate_core::{
    now_wall_ms, Category, CategoryId, CategorySource, Classifier, Error, Forge, ForgeClient, Hlc,
    PeerId, Result, Store, SyncEngine, Taxonomy,
};
use gitstate_forge::for_forge;
use gitstate_store::{db_path, SqliteStore};
use gitstate_sync::auth::ReplayGuard;
use gitstate_sync::CrdtSyncEngine;

/// Holds the forge clients. Clients are cheap (`from_env`) and constructed on
/// demand; this keeps a uniform seam the daemon and CLI both call.
#[derive(Clone, Default)]
pub struct ForgeRegistry;

impl ForgeRegistry {
    pub fn from_env() -> Self {
        ForgeRegistry
    }

    /// The right client for a forge, configured from the environment.
    pub fn client(&self, forge: Forge) -> Box<dyn ForgeClient> {
        for_forge(forge)
    }
}

/// How the management half of the API is protected.
///
/// The management API reads and writes everything this node knows — repos, paths
/// on disk, contexts, the peer list, this node's own public key. On a loopback
/// bind it is a single-user local app and needs no gate. On a routable bind it is
/// exposed to the internet, and an unprotected management API there is not a
/// weakness in sync, it is a total compromise of the node.
///
/// So the posture is explicit and there is no third state that means "probably
/// fine". [`Daemon::serve`] refuses to bind a non-loopback address while this is
/// [`AdminAuth::LocalOnly`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AdminAuth {
    /// No gate. Only valid on a loopback bind.
    LocalOnly,
    /// A bearer token every management request must carry
    /// (`GITSTATE_ADMIN_TOKEN`).
    Token(String),
    /// The operator has stated in writing that the management API is protected by
    /// something outside this process — a reverse proxy that authenticates, a
    /// private network, an SSH tunnel (`GITSTATE_ADMIN_UNAUTHENTICATED=i-accept`).
    /// Set only by an operator who typed that value; never a default and never a
    /// fallback from a failed check.
    DelegatedExternally,
}

/// The exact opt-out value. A specific phrase rather than a truthy flag, because
/// `=1` gets set by accident and `=i-accept` does not.
pub const ADMIN_UNAUTHENTICATED_OPT_IN: &str = "i-accept";

/// Everything a request handler (or CLI command) needs. Cheap to clone — every
/// field is an `Arc` or a unit.
#[derive(Clone)]
pub struct AppState {
    pub store: Arc<dyn Store>,
    pub forge: ForgeRegistry,
    pub classifier: Arc<dyn Classifier>,
    pub taxonomy: Arc<Taxonomy>,
    /// The local CRDT engine. Always present: replication needs no build feature.
    pub sync: Option<Arc<dyn SyncEngine>>,
    /// The built React app to serve statically; `None` ⇒ API only.
    pub web_dist: Option<PathBuf>,
    /// How the management API is protected. See [`AdminAuth`].
    pub admin_auth: AdminAuth,
    /// Single-use enforcement for peer request tokens. Shared across the process
    /// so a token accepted by one handler cannot be re-spent on another.
    pub replay_guard: Arc<ReplayGuard>,
}

impl AppState {
    /// Whether `header` satisfies the management-API gate.
    pub fn admin_ok(&self, header: Option<&str>) -> bool {
        match &self.admin_auth {
            AdminAuth::LocalOnly | AdminAuth::DelegatedExternally => true,
            AdminAuth::Token(expected) => header
                .and_then(|h| h.strip_prefix("Bearer "))
                .map(|got| constant_time_eq(got.trim().as_bytes(), expected.as_bytes()))
                .unwrap_or(false),
        }
    }
}

/// Length-checked, branch-free comparison of the admin token. This one really is
/// a secret, so it is not compared with `==`.
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

/// The single wiring both the `gitstated` binary and the Tauri shell use.
pub fn build_state_from_env() -> Result<AppState> {
    let data_dir = SqliteStore::data_dir()?;
    let store: Arc<dyn Store> = Arc::new(SqliteStore::open(&db_path(&data_dir))?);
    let classifier: Arc<dyn Classifier> = default_classifier().into();
    let taxonomy = Arc::new(load_taxonomy());
    // Seed the signed taxonomy's categories into the local store (once), so the
    // categories API aligns labels across peers out of the box. Local + peer
    // categories coexist alongside them.
    seed_taxonomy_categories(store.as_ref(), &taxonomy);
    let sync: Arc<dyn SyncEngine> = Arc::new(CrdtSyncEngine::from_store(store.clone())?);
    Ok(AppState {
        store,
        forge: ForgeRegistry::from_env(),
        classifier,
        taxonomy,
        // Replication is compiled in unconditionally; whether it does anything
        // depends only on whether the operator enrolled a peer.
        sync: Some(sync),
        web_dist: resolve_web_dist(),
        admin_auth: admin_auth_from_env(),
        replay_guard: Arc::new(ReplayGuard::new()),
    })
}

/// Read the management-API posture from the environment. Defaults to
/// [`AdminAuth::LocalOnly`], which [`Daemon::serve`] then refuses to pair with a
/// routable bind address.
pub fn admin_auth_from_env() -> AdminAuth {
    let token = std::env::var("GITSTATE_ADMIN_TOKEN")
        .ok()
        .map(|t| t.trim().to_string())
        .filter(|t| !t.is_empty());
    if let Some(token) = token {
        return AdminAuth::Token(token);
    }
    let opted_out = std::env::var("GITSTATE_ADMIN_UNAUTHENTICATED")
        .map(|v| v.trim() == ADMIN_UNAUTHENTICATED_OPT_IN)
        .unwrap_or(false);
    if opted_out {
        AdminAuth::DelegatedExternally
    } else {
        AdminAuth::LocalOnly
    }
}

/// Load the signed taxonomy fail-closed: honor `GITSTATE_TAXONOMY_PATH`, verify
/// against the pinned key, and fall back to the embedded default doc if
/// verification fails so the daemon still starts (categories then come from the
/// local store only — never a silently-trusted doc).
fn load_taxonomy() -> Taxonomy {
    let from_path = std::env::var("GITSTATE_TAXONOMY_PATH")
        .ok()
        .filter(|p| !p.is_empty())
        .and_then(|p| std::fs::read(p).ok());
    let loaded = match from_path {
        Some(bytes) => load_and_verify_taxonomy(Some(&bytes)),
        None => load_and_verify_taxonomy(None),
    };
    loaded.unwrap_or_else(|_| Taxonomy::default_taxonomy())
}

/// Insert any taxonomy categories the store does not yet have (keyed by their
/// stable dotted key). Idempotent: existing keys are left untouched, so this is
/// safe to run on every startup and never churns the CRDT log for unchanged
/// categories. Failures are non-fatal — a store hiccup must not stop the daemon.
fn seed_taxonomy_categories(store: &dyn Store, taxonomy: &Taxonomy) {
    for tc in &taxonomy.categories {
        match store.get_category(&tc.key) {
            Ok(Some(_)) => continue,
            Ok(None) => {}
            Err(_) => continue,
        }
        let cat = Category {
            id: CategoryId::new(),
            key: tc.key.clone(),
            label: tc.label.clone(),
            parent_key: tc.parent.clone(),
            color: tc.color.clone(),
            source: CategorySource::Taxonomy,
            taxonomy_version: Some(taxonomy.version.clone()),
            hlc: Hlc {
                wall_ms: now_wall_ms(),
                counter: 0,
                peer: PeerId::from("local"),
            },
            deleted: false,
        };
        let _ = store.upsert_category(&cat);
    }
}

/// Find the built web app. Env `GITSTATE_WEB_DIST` wins; otherwise probe the
/// usual spots relative to the executable and the working directory.
fn resolve_web_dist() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("GITSTATE_WEB_DIST") {
        let path = PathBuf::from(p);
        if path.is_dir() {
            return Some(path);
        }
    }
    let mut candidates: Vec<PathBuf> = Vec::new();
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push(dir.join("web/dist"));
            candidates.push(dir.join("../web/dist"));
            candidates.push(dir.join("../../web/dist"));
        }
    }
    if let Ok(cwd) = std::env::current_dir() {
        candidates.push(cwd.join("web/dist"));
        candidates.push(cwd.join("../web/dist"));
    }
    candidates
        .into_iter()
        .find(|p| p.join("index.html").is_file())
}

/// Binds a socket and serves the router. One instance backs both headless and
/// desktop modes.
pub struct Daemon {
    state: AppState,
}

impl Daemon {
    pub fn new(state: AppState) -> Self {
        Daemon { state }
    }

    /// The composed router (API + static + CORS). Handy for tests.
    pub fn router(&self) -> Router {
        router_for(self.state.clone())
    }

    /// Serve on an explicit address until the process ends.
    ///
    /// Refuses to bind a routable address while the management API has no gate.
    /// The check is here rather than in the handler because a management API that
    /// is reachable is already lost: by the time a request arrives the operator
    /// has already published it. Failing at bind time makes the mistake
    /// impossible to make quietly.
    pub async fn serve(self, addr: SocketAddr) -> Result<()> {
        guard_exposed_bind(&addr, &self.state.admin_auth)?;
        let listener = tokio::net::TcpListener::bind(addr)
            .await
            .map_err(|e| Error::Http(format!("bind {addr}: {e}")))?;
        let router = router_for(self.state);
        axum::serve(listener, router.into_make_service())
            .await
            .map_err(|e| Error::Http(format!("serve: {e}")))?;
        Ok(())
    }

    /// Bind `127.0.0.1:0`, return the chosen address, and serve in a background
    /// task. Used by the Tauri shell: the webview then points at the returned
    /// address.
    pub async fn serve_ephemeral(self) -> Result<(SocketAddr, JoinHandle<()>)> {
        let listener = tokio::net::TcpListener::bind((Ipv4Addr::LOCALHOST, 0))
            .await
            .map_err(|e| Error::Http(format!("bind ephemeral: {e}")))?;
        let addr = listener
            .local_addr()
            .map_err(|e| Error::Http(format!("local_addr: {e}")))?;
        let router = router_for(self.state);
        let handle = tokio::spawn(async move {
            let _ = axum::serve(listener, router.into_make_service()).await;
        });
        Ok((addr, handle))
    }
}

fn router_for(state: AppState) -> Router {
    crate::router::build_router(state)
}

/// The bind-time fail-closed guard. Separate from [`Daemon::serve`] so it can be
/// unit-tested without opening a socket.
pub fn guard_exposed_bind(addr: &SocketAddr, auth: &AdminAuth) -> Result<()> {
    if addr.ip().is_loopback() || *auth != AdminAuth::LocalOnly {
        return Ok(());
    }
    Err(Error::invalid(format!(
        "refusing to bind {addr}: that address is reachable from outside this machine, and the \
         management API has no authentication. Set GITSTATE_ADMIN_TOKEN=<secret> so management \
         requests must carry `Authorization: Bearer <secret>`, or — if something in front of \
         gitstate already authenticates them — set \
         GITSTATE_ADMIN_UNAUTHENTICATED={ADMIN_UNAUTHENTICATED_OPT_IN}. See docs/DEPLOYMENT.md. \
         (The peer endpoints /api/sync/pull and /api/sync/push are always key-authenticated and \
         are not what this guard is about.)"
    )))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::{IpAddr, Ipv6Addr};

    fn addr(ip: &str, port: u16) -> SocketAddr {
        SocketAddr::new(ip.parse::<IpAddr>().unwrap(), port)
    }

    #[test]
    fn a_loopback_bind_needs_no_admin_gate() {
        guard_exposed_bind(&addr("127.0.0.1", 7473), &AdminAuth::LocalOnly).unwrap();
        guard_exposed_bind(
            &SocketAddr::new(IpAddr::V6(Ipv6Addr::LOCALHOST), 7473),
            &AdminAuth::LocalOnly,
        )
        .unwrap();
    }

    /// The whole point: `GITSTATE_ADDR=0.0.0.0` with no token does not start.
    #[test]
    fn a_routable_bind_without_a_gate_is_refused() {
        for ip in ["0.0.0.0", "10.0.0.4", "203.0.113.7", "::"] {
            let err = guard_exposed_bind(&addr(ip, 7473), &AdminAuth::LocalOnly)
                .expect_err("{ip} must be refused");
            assert!(
                format!("{err}").contains("GITSTATE_ADMIN_TOKEN"),
                "the refusal must say how to fix it: {err}"
            );
        }
    }

    #[test]
    fn a_routable_bind_is_allowed_once_the_operator_has_chosen_a_posture() {
        guard_exposed_bind(&addr("0.0.0.0", 7473), &AdminAuth::Token("s3cret".into())).unwrap();
        guard_exposed_bind(&addr("0.0.0.0", 7473), &AdminAuth::DelegatedExternally).unwrap();
    }

    #[test]
    fn the_bearer_token_must_match_exactly() {
        let mut state_auth = AdminAuth::Token("s3cret".into());
        let ok = |auth: &AdminAuth, header: Option<&str>| -> bool {
            match auth {
                AdminAuth::Token(expected) => header
                    .and_then(|h| h.strip_prefix("Bearer "))
                    .map(|got| constant_time_eq(got.trim().as_bytes(), expected.as_bytes()))
                    .unwrap_or(false),
                _ => true,
            }
        };
        assert!(ok(&state_auth, Some("Bearer s3cret")));
        assert!(!ok(&state_auth, Some("Bearer s3cre")));
        assert!(!ok(&state_auth, Some("Bearer s3cretx")));
        assert!(!ok(&state_auth, Some("s3cret")));
        assert!(!ok(&state_auth, None));
        state_auth = AdminAuth::LocalOnly;
        assert!(ok(&state_auth, None), "no gate on loopback");
    }
}
