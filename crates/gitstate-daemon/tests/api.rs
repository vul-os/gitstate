//! End-to-end router tests over an in-memory store: prove the §3 contract shape
//! for the endpoints that don't require a real git worktree or forge.

use std::sync::Arc;

use axum::body::Body;
use axum::http::{Request, StatusCode};
use tower::ServiceExt;

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
