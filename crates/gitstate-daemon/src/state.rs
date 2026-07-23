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

/// Everything a request handler (or CLI command) needs. Cheap to clone — every
/// field is an `Arc` or a unit.
#[derive(Clone)]
pub struct AppState {
    pub store: Arc<dyn Store>,
    pub forge: ForgeRegistry,
    pub classifier: Arc<dyn Classifier>,
    pub taxonomy: Arc<Taxonomy>,
    /// `None` unless built with the `sync-dmtap` feature and wired.
    pub sync: Option<Arc<dyn SyncEngine>>,
    /// The built React app to serve statically; `None` ⇒ API only.
    pub web_dist: Option<PathBuf>,
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
    Ok(AppState {
        store,
        forge: ForgeRegistry::from_env(),
        classifier,
        taxonomy,
        sync: None,
        web_dist: resolve_web_dist(),
    })
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
    pub async fn serve(self, addr: SocketAddr) -> Result<()> {
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
