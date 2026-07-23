//! End-to-end router tests over an in-memory store: prove the §3 contract shape
//! for the endpoints that don't require a real git worktree or forge.

use std::sync::Arc;

use axum::body::Body;
use axum::http::{Request, StatusCode};
use tower::ServiceExt;

use gitstate_core::{Commit, Forge, Repo, RepoId, WorkItem, WorkItemId, WorkKind, WorkState};
use gitstate_daemon::{AppState, Daemon, ForgeRegistry};

fn state() -> AppState {
    let store = Arc::new(gitstate_store::SqliteStore::open_in_memory().unwrap());
    AppState {
        store,
        forge: ForgeRegistry::from_env(),
        classifier: gitstate_classify::default_classifier().into(),
        taxonomy: Arc::new(gitstate_core::Taxonomy::default_taxonomy()),
        sync: None,
        web_dist: None,
    }
}

async fn json(resp: axum::response::Response) -> serde_json::Value {
    let bytes = axum::body::to_bytes(resp.into_body(), usize::MAX)
        .await
        .unwrap();
    serde_json::from_slice(&bytes).unwrap()
}

fn get(uri: &str) -> Request<Body> {
    Request::builder().uri(uri).body(Body::empty()).unwrap()
}

fn post(uri: &str, body: serde_json::Value) -> Request<Body> {
    Request::builder()
        .method("POST")
        .uri(uri)
        .header("content-type", "application/json")
        .body(Body::from(body.to_string()))
        .unwrap()
}

#[tokio::test]
async fn health_reports_contract_shape() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/health"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    assert_eq!(v["status"], "ok");
    assert_eq!(v["version"], "0.1.0");
    assert_eq!(v["sync"], false);
    assert_eq!(v["classifier"], "heuristic");
}

#[tokio::test]
async fn repos_start_empty() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/repos"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    assert_eq!(v.as_array().unwrap().len(), 0);
}

#[tokio::test]
async fn context_create_and_list() {
    let st = state();

    let resp = Daemon::new(st.clone())
        .router()
        .oneshot(post(
            "/api/contexts",
            serde_json::json!({ "name": "Q3 refactor", "tags": ["refactor"] }),
        ))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::CREATED);
    let created = json(resp).await;
    assert_eq!(created["name"], "Q3 refactor");
    assert!(created["id"].as_str().is_some());

    let resp = Daemon::new(st)
        .router()
        .oneshot(get("/api/contexts"))
        .await
        .unwrap();
    let list = json(resp).await;
    assert_eq!(list.as_array().unwrap().len(), 1);
    assert_eq!(list[0]["tags"][0], "refactor");
}

#[tokio::test]
async fn taxonomy_served_and_categories_present() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/taxonomy"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    assert_eq!(v["schema"], "gitstate.taxonomy/v1");
    assert!(v["categories"].as_array().unwrap().len() >= 19);
}

#[tokio::test]
async fn sync_status_disabled_by_default() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/sync/status"))
        .await
        .unwrap();
    let v = json(resp).await;
    assert_eq!(v["enabled"], false);
    assert_eq!(v["peers"], 0);
}

#[tokio::test]
async fn missing_project_state_is_404() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/repos/does-not-exist/project-state"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::NOT_FOUND);
    let v = json(resp).await;
    assert_eq!(v["code"], "not_found");
}

// ─────────────────────────────── analytics ───────────────────────────────

/// Seed a repo with commits + one merged PR so the analytics rollup has shape.
fn seed_activity(st: &AppState) -> RepoId {
    let repo_id = RepoId::from("repo-1");
    st.store
        .upsert_repo(&Repo {
            id: repo_id.clone(),
            slug: "demo/analytics".into(),
            path: String::new(),
            remote_url: None,
            forge: Forge::Local,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: "2026-01-01T00:00:00Z".into(),
        })
        .unwrap();

    let commits: Vec<Commit> = [
        ("aaa", "2026-06-10T10:00:00Z", 40, 5),
        ("bbb", "2026-06-10T16:00:00Z", 10, 1),
        ("ccc", "2026-06-12T09:00:00Z", 25, 30),
    ]
    .into_iter()
    .map(|(sha, at, add, del)| Commit {
        sha: sha.into(),
        repo_id: repo_id.clone(),
        author_email: "dev@example.com".into(),
        author_name: "Dev".into(),
        committed_at: at.into(),
        additions: add,
        deletions: del,
        files_changed: 2,
        is_merge: false,
        is_test_touch: false,
        summary: "work".into(),
    })
    .collect();
    st.store.save_commits(&repo_id, &commits).unwrap();

    st.store
        .save_work_items(&[WorkItem {
            id: WorkItemId::from("wi-1".to_string()),
            repo_id: repo_id.clone(),
            kind: WorkKind::Pr,
            external_ref: "#7".into(),
            title: "Add analytics".into(),
            body: String::new(),
            state: WorkState::Merged,
            author_login: Some("dev".into()),
            labels: vec!["backend".into()],
            created_at: "2026-06-09T10:00:00Z".into(),
            updated_at: "2026-06-11T10:00:00Z".into(),
            merged_at: Some("2026-06-11T10:00:00Z".into()),
            closed_at: Some("2026-06-11T10:00:00Z".into()),
            files_touched: vec!["src/lib.rs".into()],
        }])
        .unwrap();

    repo_id
}

#[tokio::test]
async fn analytics_on_an_empty_store_still_returns_a_grid() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/analytics?days=30"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    assert_eq!(v["totals"]["commits"], 0);
    // A dense, zero-filled grid renders an empty state instead of collapsing.
    assert_eq!(v["range"]["days"], 30);
    assert_eq!(v["heatmap"].as_array().unwrap().len(), 30);
}

#[tokio::test]
async fn analytics_rolls_up_commits_and_work_items() {
    let st = state();
    seed_activity(&st);

    let resp = Daemon::new(st)
        .router()
        .oneshot(get("/api/analytics?days=30"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;

    assert_eq!(v["totals"]["commits"], 3);
    assert_eq!(v["totals"]["repos"], 1);
    assert_eq!(v["totals"]["contributors"], 1);
    assert_eq!(v["totals"]["additions"], 75);
    assert_eq!(v["totals"]["deletions"], 36);
    assert_eq!(v["totals"]["net_lines"], 39);
    assert_eq!(v["totals"]["active_days"], 2);
    assert_eq!(v["totals"]["merged_prs"], 1);

    // The window anchors on the newest commit, not wall-clock now, so a stale
    // database still renders a populated heatmap.
    assert_eq!(v["range"]["to"], "2026-06-12");
    assert_eq!(v["heatmap"].as_array().unwrap().len(), 30);
    let busiest = v["heatmap"]
        .as_array()
        .unwrap()
        .iter()
        .map(|d| d["commits"].as_u64().unwrap())
        .max()
        .unwrap();
    assert_eq!(busiest, 2);

    assert_eq!(v["contributors"][0]["email"], "dev@example.com");
    assert_eq!(v["contributors"][0]["commits"], 3);
    assert_eq!(v["cycle_time"].as_array().unwrap().len(), 1);
    assert_eq!(v["cycle_time"][0]["hours"], 48.0);
    assert_eq!(v["labels"][0]["key"], "backend");
}

#[tokio::test]
async fn analytics_scopes_to_a_single_repo() {
    let st = state();
    seed_activity(&st);

    // A second repo with its own commit must not leak into the scoped view.
    let other = RepoId::from("repo-2");
    st.store
        .upsert_repo(&Repo {
            id: other.clone(),
            slug: "demo/other".into(),
            path: String::new(),
            remote_url: None,
            forge: Forge::Local,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: "2026-01-01T00:00:00Z".into(),
        })
        .unwrap();
    st.store
        .save_commits(
            &other,
            &[Commit {
                sha: "zzz".into(),
                repo_id: other.clone(),
                author_email: "other@example.com".into(),
                author_name: "Other".into(),
                committed_at: "2026-06-11T10:00:00Z".into(),
                additions: 999,
                deletions: 0,
                files_changed: 1,
                is_merge: false,
                is_test_touch: false,
                summary: "other".into(),
            }],
        )
        .unwrap();

    let all = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(get("/api/analytics?days=30"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(all["totals"]["commits"], 4);
    assert_eq!(all["totals"]["repos"], 2);

    let scoped = json(
        Daemon::new(st)
            .router()
            .oneshot(get("/api/analytics?days=30&repo_id=repo-1"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(scoped["totals"]["commits"], 3);
    assert_eq!(scoped["totals"]["repos"], 1);
    assert_eq!(scoped["totals"]["contributors"], 1);
}

#[tokio::test]
async fn analytics_honors_an_explicit_from_bound() {
    let st = state();
    seed_activity(&st);

    let v = json(
        Daemon::new(st)
            .router()
            .oneshot(get("/api/analytics?from=2026-06-11&to=2026-06-12"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(v["range"]["from"], "2026-06-11");
    assert_eq!(v["range"]["days"], 2);
    assert_eq!(v["totals"]["commits"], 1, "the 06-10 pair falls outside");
}

#[tokio::test]
async fn analytics_clamps_an_absurd_window() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(get("/api/analytics?days=99999999"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    assert_eq!(v["range"]["days"], gitstate_daemon::ops::MAX_ANALYTICS_DAYS);
}
