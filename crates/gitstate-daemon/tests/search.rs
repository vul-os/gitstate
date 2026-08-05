//! `gitstate_daemon::ops::search` / `ops::embed_pending` (T11 port plan,
//! wave 4). No daemon HTTP route consumes these (see the module doc on the
//! `search` section of `ops.rs` — no page in `web/` calls anything
//! resembling `/api/search`), so they're exercised directly against a real
//! in-memory `SqliteStore`, the same shape `tests/context_bundle.rs` uses
//! for its own routeless domain.
//!
//! The actual ranking logic (FTS5/vector-KNN/RRF/fuzzy) is already proven
//! against a real store in `gitstate-search`'s own
//! `tests/search_against_real_store.rs`; this file's job is narrower and
//! different: prove the `ops::search`/`ops::embed_pending` wrappers plumb
//! `AppState` through correctly, end to end, the way an MCP tool call or a
//! CLI invocation actually will.

use std::sync::Arc;

use gitstate_core::{now_rfc3339, Forge, Repo, RepoId, SearchKind, WorkItem, WorkItemId, WorkKind, WorkState};
use gitstate_daemon::{ops, AppState, ForgeRegistry};

fn state() -> AppState {
    let store = Arc::new(gitstate_store::SqliteStore::open_in_memory().unwrap());
    AppState {
        store,
        forge: ForgeRegistry::from_env(),
        classifier: gitstate_classify::default_classifier().into(),
        taxonomy: Arc::new(gitstate_core::Taxonomy::default_taxonomy()),
        sync: None,
        web_dist: None,
        admin_auth: gitstate_daemon::AdminAuth::LocalOnly,
        replay_guard: Arc::new(gitstate_sync::auth::ReplayGuard::new()),
    }
}

fn seed_repo(state: &AppState, id: &str) -> RepoId {
    let rid = RepoId(id.to_string());
    state
        .store
        .upsert_repo(&Repo {
            id: rid.clone(),
            slug: format!("demo/{id}"),
            path: String::new(),
            remote_url: None,
            forge: Forge::Local,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: now_rfc3339(),
        })
        .unwrap();
    rid
}

fn issue(repo: &RepoId, id: &str, title: &str, body: &str) -> WorkItem {
    WorkItem {
        id: WorkItemId(id.to_string()),
        repo_id: repo.clone(),
        kind: WorkKind::Issue,
        external_ref: "#1".into(),
        title: title.into(),
        body: body.into(),
        state: WorkState::Open,
        author_login: None,
        labels: vec![],
        created_at: now_rfc3339(),
        updated_at: now_rfc3339(),
        merged_at: None,
        closed_at: None,
        files_touched: vec![],
    }
}

#[test]
fn ops_search_finds_an_fts_hit_through_appstate() {
    let s = state();
    let repo = seed_repo(&s, "r1");
    s.store
        .save_work_items(&[issue(
            &repo,
            "iss-1",
            "Fix authentication redirect loop",
            "users cannot log in",
        )])
        .unwrap();

    let outcome = ops::search(&s, "authentication", &[SearchKind::Issue], 20).unwrap();
    assert_eq!(outcome.results.len(), 1);
    assert_eq!(outcome.results[0].id, "iss-1");
    assert!(!outcome.semantic);
    assert!(!outcome.fuzzy);
}

#[test]
fn ops_embed_pending_then_ops_search_finds_a_semantic_hit() {
    let s = state();
    let repo = seed_repo(&s, "r1");
    s.store
        .save_work_items(&[issue(
            &repo,
            "iss-1",
            "Fix authentication redirect loop",
            "users cannot log in",
        )])
        .unwrap();

    let embedded = ops::embed_pending(&s, 0).unwrap();
    assert_eq!(embedded, 1);
    // Idempotent — a fresh row is not re-embedded.
    assert_eq!(ops::embed_pending(&s, 0).unwrap(), 0);

    // A typo FTS cannot match; the embedder's trigram robustness still ranks
    // the right issue first via vector KNN.
    let outcome = ops::search(&s, "authentcation redirect", &[SearchKind::Issue], 20).unwrap();
    assert!(outcome.semantic);
    assert_eq!(outcome.results[0].id, "iss-1");
    assert_eq!(outcome.results[0].title, "Fix authentication redirect loop");
}

#[test]
fn ops_search_empty_query_is_empty_not_an_error() {
    let s = state();
    let outcome = ops::search(&s, "   ", &[], 20).unwrap();
    assert!(outcome.results.is_empty());
}
