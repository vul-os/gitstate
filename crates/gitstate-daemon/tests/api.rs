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

// ───────────────────── weights / trackers / import ─────────────────────

fn put(uri: &str, body: serde_json::Value) -> Request<Body> {
    Request::builder()
        .method("PUT")
        .uri(uri)
        .header("content-type", "application/json")
        .body(Body::from(body.to_string()))
        .unwrap()
}

fn delete(uri: &str) -> Request<Body> {
    Request::builder()
        .method("DELETE")
        .uri(uri)
        .body(Body::empty())
        .unwrap()
}

/// Register a repo so imports have somewhere to land.
fn seed_repo(st: &AppState, id: &str) -> RepoId {
    let rid = RepoId::from(id.to_string());
    st.store
        .upsert_repo(&Repo {
            id: rid.clone(),
            slug: format!("demo/{id}"),
            path: String::new(),
            remote_url: None,
            forge: Forge::Local,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: "2026-01-01T00:00:00Z".into(),
        })
        .unwrap();
    rid
}

#[tokio::test]
async fn weights_default_then_normalize_on_put_then_reset() {
    let st = state();

    let v = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(get("/api/weights"))
            .await
            .unwrap(),
    )
    .await;
    assert!(v["shipped"].as_f64().unwrap() > 0.0);

    // Unnormalized input comes back summing to 1, so the UI shows exactly what
    // the composite is actually computed with.
    let resp = Daemon::new(st.clone())
        .router()
        .oneshot(put(
            "/api/weights",
            serde_json::json!({
                "shipped": 2.0, "review": 2.0, "effort": 2.0,
                "quality": 2.0, "ownership": 1.0, "durability": 1.0
            }),
        ))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let v = json(resp).await;
    let sum: f64 = [
        "shipped",
        "review",
        "effort",
        "quality",
        "ownership",
        "durability",
    ]
    .iter()
    .map(|k| v[k].as_f64().unwrap())
    .sum();
    assert!(
        (sum - 1.0).abs() < 1e-9,
        "weights should normalize, summed {sum}"
    );

    // And they persist.
    let v = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(get("/api/weights"))
            .await
            .unwrap(),
    )
    .await;
    assert!((v["shipped"].as_f64().unwrap() - 0.2).abs() < 1e-9);

    let v = json(
        Daemon::new(st)
            .router()
            .oneshot(post("/api/weights/reset", serde_json::json!({})))
            .await
            .unwrap(),
    )
    .await;
    assert!(v["shipped"].as_f64().is_some());
}

#[tokio::test]
async fn weights_reject_negative_and_zero_sum() {
    for bad in [
        serde_json::json!({"shipped":-1.0,"review":1.0,"effort":1.0,"quality":1.0,"ownership":1.0,"durability":1.0}),
        serde_json::json!({"shipped":0.0,"review":0.0,"effort":0.0,"quality":0.0,"ownership":0.0,"durability":0.0}),
    ] {
        let resp = Daemon::new(state())
            .router()
            .oneshot(put("/api/weights", bad))
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    }
}

#[tokio::test]
async fn a_saved_tracker_token_can_never_be_read_back() {
    let st = state();
    let secret = "super-secret-token-9999";

    let resp = Daemon::new(st.clone())
        .router()
        .oneshot(put(
            "/api/trackers/jira",
            serde_json::json!({
                "base_url": "https://acme.atlassian.net",
                "email": "dev@example.com",
                "token": secret,
                "project": "ENG"
            }),
        ))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);
    let saved = json(resp).await;
    assert_ne!(
        saved["token"], secret,
        "the write response must not echo the secret"
    );

    let list = json(
        Daemon::new(st)
            .router()
            .oneshot(get("/api/trackers"))
            .await
            .unwrap(),
    )
    .await;
    let body = list.to_string();
    assert!(
        !body.contains(secret),
        "the token leaked through /api/trackers: {body}"
    );
    let jira = list
        .as_array()
        .unwrap()
        .iter()
        .find(|t| t["kind"] == "jira")
        .unwrap();
    assert_eq!(jira["configured"], true);
    assert_eq!(jira["base_url"], "https://acme.atlassian.net");
    assert_eq!(jira["token"], "…9999", "only a masked hint is exposed");
}

#[tokio::test]
async fn an_empty_token_edit_preserves_the_stored_secret() {
    let st = state();
    Daemon::new(st.clone())
        .router()
        .oneshot(put(
            "/api/trackers/linear",
            serde_json::json!({ "token": "lin_api_abcd", "project": "ENG" }),
        ))
        .await
        .unwrap();

    // Edit only the team key — the UI must not force a re-paste of the secret.
    let v = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(put(
                "/api/trackers/linear",
                serde_json::json!({ "token": "", "project": "OPS" }),
            ))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(v["project"], "OPS");
    assert_eq!(v["configured"], true, "the credential survived the edit");
    assert_eq!(v["token"], "…abcd");
}

#[tokio::test]
async fn deleting_a_tracker_clears_it() {
    let st = state();
    Daemon::new(st.clone())
        .router()
        .oneshot(put(
            "/api/trackers/jira",
            serde_json::json!({ "token": "t" }),
        ))
        .await
        .unwrap();

    let resp = Daemon::new(st.clone())
        .router()
        .oneshot(delete("/api/trackers/jira"))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::OK);

    let list = json(
        Daemon::new(st)
            .router()
            .oneshot(get("/api/trackers"))
            .await
            .unwrap(),
    )
    .await;
    let jira = list
        .as_array()
        .unwrap()
        .iter()
        .find(|t| t["kind"] == "jira")
        .unwrap();
    assert_eq!(jira["configured"], false);
    assert_eq!(jira["token"], "");
}

#[tokio::test]
async fn an_unknown_tracker_kind_is_a_400() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(put(
            "/api/trackers/asana",
            serde_json::json!({ "token": "x" }),
        ))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    let v = json(resp).await;
    assert_eq!(v["code"], "invalid");
}

#[tokio::test]
async fn import_from_a_jira_csv_export_persists_work_items() {
    let st = state();
    let repo = seed_repo(&st, "repo-import");

    let csv = "Issue key,Summary,Status,Assignee,Labels,Created,Updated,Resolved\n\
               ENG-1,Fix the parser,Done,ada@example.com,\"bug,parser\",2026-05-01,2026-06-01,2026-06-01\n\
               ENG-2,Add pagination,In Progress,femi@example.com,api,2026-05-02,2026-06-02,\n";

    let v = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(post(
                "/api/import/file",
                serde_json::json!({ "source": "jira", "repo_id": repo.0, "content": csv }),
            ))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(v["imported"], 2);

    let items = st.store.list_work_items(&repo).unwrap();
    assert_eq!(items.len(), 2);
    // Tracker tickets are issues, never PRs — otherwise they would corrupt the
    // cycle-time series, which measures open→merge on pull requests.
    assert!(items.iter().all(|w| w.kind == WorkKind::Issue));
    assert!(items.iter().all(|w| w.merged_at.is_none()));
    let done = items.iter().find(|w| w.external_ref == "ENG-1").unwrap();
    assert_eq!(done.state, WorkState::Done);
    assert_eq!(done.labels, vec!["bug".to_string(), "parser".to_string()]);
}

#[tokio::test]
async fn reimporting_the_same_export_updates_in_place() {
    let st = state();
    let repo = seed_repo(&st, "repo-idem");
    let json_export = serde_json::json!({
        "issues": [{
            "key": "ENG-9",
            "fields": {
                "summary": "Ship it",
                "status": { "statusCategory": { "key": "done" } },
                "created": "2026-05-01T09:00:00.000+0000",
                "updated": "2026-06-01T09:00:00.000+0000"
            }
        }]
    })
    .to_string();

    for _ in 0..3 {
        Daemon::new(st.clone())
            .router()
            .oneshot(post(
                "/api/import/file",
                serde_json::json!({ "repo_id": repo.0, "content": json_export }),
            ))
            .await
            .unwrap();
    }

    let items = st.store.list_work_items(&repo).unwrap();
    assert_eq!(
        items.len(),
        1,
        "ids are deterministic, so syncs must not duplicate"
    );
}

#[tokio::test]
async fn importing_into_a_missing_repo_is_404() {
    let resp = Daemon::new(state())
        .router()
        .oneshot(post(
            "/api/import/file",
            serde_json::json!({ "repo_id": "nope", "content": "Issue key,Summary\nE-1,x\n" }),
        ))
        .await
        .unwrap();
    assert_eq!(resp.status(), StatusCode::NOT_FOUND);
    assert_eq!(json(resp).await["code"], "not_found");
}

#[tokio::test]
async fn importing_from_an_unconfigured_tracker_explains_itself() {
    let st = state();
    let repo = seed_repo(&st, "repo-unconfigured");
    let resp = Daemon::new(st)
        .router()
        .oneshot(post(
            "/api/import/run",
            serde_json::json!({ "kind": "jira", "repo_id": repo.0 }),
        ))
        .await
        .unwrap();
    // No credential ⇒ a clear 400, not a network attempt or a 500.
    assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    let msg = json(resp).await["error"].as_str().unwrap().to_string();
    assert!(msg.contains("not configured"), "{msg}");
}

// ─────────────────── health metrics / involvement ───────────────────

#[tokio::test]
async fn health_metrics_on_an_empty_store_are_finite() {
    let v = json(
        Daemon::new(state())
            .router()
            .oneshot(get("/api/health-metrics?days=30"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(v["bus_factor"]["count"], 0);
    assert_eq!(v["review"]["reviewed_pr_share"], 0.0);
    assert_eq!(v["quality"]["test_touch_rate"], 0.0);
    assert!(v["dora"]["cycle_p50_hours"].is_null());
}

#[tokio::test]
async fn health_metrics_and_involvement_reflect_seeded_activity() {
    let st = state();
    seed_activity(&st);

    let v = json(
        Daemon::new(st.clone())
            .router()
            .oneshot(get("/api/health-metrics?days=30"))
            .await
            .unwrap(),
    )
    .await;
    // One author holds every commit ⇒ bus factor 1, full concentration.
    assert_eq!(v["bus_factor"]["count"], 1);
    assert_eq!(v["bus_factor"]["top_share"], 1.0);
    assert_eq!(v["review"]["merged_prs"], 1);
    assert_eq!(v["review"]["unreviewed_merged"], 1);
    assert!(v["dora"]["cycle_p50_hours"].as_f64().unwrap() > 0.0);

    let inv = json(
        Daemon::new(st)
            .router()
            .oneshot(get("/api/involvement?days=30"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(inv["repos"].as_array().unwrap().len(), 1);
    assert_eq!(inv["people"].as_array().unwrap().len(), 1);
    assert_eq!(inv["people"][0]["total_commits"], 3);
    assert_eq!(inv["people"][0]["repo_count"], 1);
}
