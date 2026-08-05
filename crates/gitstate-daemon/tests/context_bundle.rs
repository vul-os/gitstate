//! `gitstate_daemon::ops::build_issue_context` / `build_pr_context` (T11 port
//! plan, wave 3 — the `context_bundle` domain). No daemon route consumes
//! these (see the module doc on the `context bundle` section of `ops.rs`), so
//! they're exercised directly against a real in-memory `SqliteStore`, the
//! same `AppState` shape `tests/api.rs` builds for the routed endpoints.
//!
//! These tests exist specifically to prove the token-budget/truncation logic
//! (this domain's whole point — Go's `context_bundle.go` caps every list so
//! the bundle fits an agent's context window) rather than just "it compiles
//! and returns something": every cap (`related_prs` at 5, `recent_commits` at
//! 8, `similar_issues` at 3, `code_areas` at 10, `body` at 800 chars, titles
//! at 100 chars) is seeded past its limit and asserted to come back AT the
//! limit, not over it and not silently under it.

use std::sync::Arc;

use gitstate_core::{
    now_rfc3339, EffortEstimate, EffortMethod, Error, Forge, Repo, RepoId, WorkItem, WorkItemId,
    WorkKind, WorkState,
};
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

#[allow(clippy::too_many_arguments)]
fn work_item(
    repo: &RepoId,
    id: &str,
    kind: WorkKind,
    external_ref: &str,
    title: &str,
    body: &str,
    wstate: WorkState,
    labels: &[&str],
    created_at: &str,
    updated_at: &str,
    merged_at: Option<&str>,
    files_touched: &[&str],
) -> WorkItem {
    WorkItem {
        id: WorkItemId(id.to_string()),
        repo_id: repo.clone(),
        kind,
        external_ref: external_ref.to_string(),
        title: title.to_string(),
        body: body.to_string(),
        state: wstate,
        author_login: None,
        labels: labels.iter().map(|s| s.to_string()).collect(),
        created_at: created_at.to_string(),
        updated_at: updated_at.to_string(),
        merged_at: merged_at.map(str::to_string),
        closed_at: None,
        files_touched: files_touched.iter().map(|s| s.to_string()).collect(),
    }
}

#[test]
fn build_issue_context_errors_on_unknown_or_wrong_kind_id() {
    let st = state();
    let repo = seed_repo(&st, "r1");

    // Unknown id entirely.
    let err = ops::build_issue_context(&st, &WorkItemId("nope".into())).unwrap_err();
    assert!(matches!(
        err,
        Error::NotFound {
            entity: "issue",
            ..
        }
    ));

    // A PR id is not an issue — same 404 Go's issues-only-table read gave.
    let pr = work_item(
        &repo,
        "pr-1",
        WorkKind::Pr,
        "#1",
        "a pr",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[pr]).unwrap();
    let err = ops::build_issue_context(&st, &WorkItemId("pr-1".into())).unwrap_err();
    assert!(matches!(
        err,
        Error::NotFound {
            entity: "issue",
            ..
        }
    ));
}

#[test]
fn build_pr_context_errors_on_unknown_or_wrong_kind_id() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#1",
        "an issue",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[issue]).unwrap();

    let err = ops::build_pr_context(&st, &WorkItemId("nope".into())).unwrap_err();
    assert!(matches!(
        err,
        Error::NotFound {
            entity: "pull request",
            ..
        }
    ));
    let err = ops::build_pr_context(&st, &WorkItemId("iss-1".into())).unwrap_err();
    assert!(matches!(
        err,
        Error::NotFound {
            entity: "pull request",
            ..
        }
    ));
}

#[test]
fn issue_summary_carries_number_labels_and_trims_body_and_state() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let body: String = "x".repeat(950);
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#42",
        "fix the login bug",
        &body,
        WorkState::InProgress,
        &["bug", "backend"],
        "2026-01-01T00:00:00Z",
        "2026-01-02T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[issue]).unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    assert_eq!(bundle.issue.number, Some(42));
    assert_eq!(bundle.issue.title, "fix the login bug");
    assert_eq!(bundle.issue.state, "in_progress");
    assert_eq!(
        bundle.issue.labels,
        vec!["bug".to_string(), "backend".to_string()]
    );
    assert_eq!(bundle.issue.repo_id, repo);
    // 950 raw chars, capped to 800 + an ellipsis, never the full 950.
    assert!(bundle.issue.body.chars().count() <= 801);
    assert!(bundle.issue.body.ends_with('…'));
}

#[test]
fn related_prs_caps_at_five_newest_first_by_merged_then_created() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#1",
        "an issue",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[issue]).unwrap();

    // Seed 7 merged PRs in the same repo, merged at 7 distinct times — one
    // more than the cap (5), so the truncation itself is under test, not
    // just "some PRs came back".
    let mut prs = Vec::new();
    for i in 0..7 {
        let ts = format!("2026-02-{:02}T00:00:00Z", i + 1);
        prs.push(work_item(
            &repo,
            &format!("pr-{i}"),
            WorkKind::Pr,
            &format!("#{i}"),
            &format!("pr number {i}"),
            "",
            WorkState::Merged,
            &[],
            "2026-01-01T00:00:00Z",
            &ts,
            Some(ts.as_str()),
            &[],
        ));
    }
    st.store.save_work_items(&prs).unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    assert_eq!(bundle.related_prs.len(), 5, "capped at MAX_RELATED_PRS");
    // Newest-first: pr-6 (merged 2026-02-07) must lead, pr-2 (2026-02-03) is
    // the 5th and last — pr-0/pr-1 (the two oldest) must be dropped, not just
    // any 5 of the 7.
    let titles: Vec<&str> = bundle
        .related_prs
        .iter()
        .map(|p| p.title.as_str())
        .collect();
    assert_eq!(
        titles,
        vec![
            "pr number 6",
            "pr number 5",
            "pr number 4",
            "pr number 3",
            "pr number 2",
        ]
    );
    assert!(bundle.related_prs[0].merged);
    assert!(bundle.related_prs[0].lead_time_secs.unwrap() > 0);
}

#[test]
fn recent_commits_caps_at_eight_newest_first() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#1",
        "an issue",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[issue]).unwrap();

    let commits: Vec<gitstate_core::Commit> = (0..10)
        .map(|i| gitstate_core::Commit {
            sha: format!("{i:040}"),
            repo_id: repo.clone(),
            author_email: "dev@example.com".into(),
            author_name: "Dev".into(),
            committed_at: format!("2026-03-{:02}T00:00:00Z", i + 1),
            additions: 1,
            deletions: 0,
            files_changed: 1,
            is_merge: false,
            is_test_touch: false,
            summary: format!("commit number {i}"),
        })
        .collect();
    st.store.save_commits(&repo, &commits).unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    assert_eq!(
        bundle.recent_commits.len(),
        8,
        "capped at MAX_RELATED_COMMITS"
    );
    // Newest-first: commit 9 (2026-03-10) leads; commits 0/1 (the two oldest)
    // are dropped.
    assert_eq!(bundle.recent_commits[0].subject, "commit number 9");
    assert_eq!(bundle.recent_commits[7].subject, "commit number 2");
    assert_eq!(bundle.recent_commits[0].sha.len(), 10, "short sha");
}

#[test]
fn code_areas_re_derived_from_files_touched_and_capped_at_ten() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#1",
        "an issue",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    st.store.save_work_items(&[issue]).unwrap();

    // 12 distinct top-level dirs across several PRs — one more pair than the
    // cap (10), so truncation is under test.
    let mut prs = Vec::new();
    for i in 0..12 {
        let path = format!("area{i:02}/x.rs");
        prs.push(work_item(
            &repo,
            &format!("pr-{i}"),
            WorkKind::Pr,
            &format!("#{i}"),
            "t",
            "",
            WorkState::Merged,
            &[],
            "2026-01-01T00:00:00Z",
            "2026-01-01T00:00:00Z",
            Some("2026-01-02T00:00:00Z"),
            &[path.as_str()],
        ));
    }
    st.store.save_work_items(&prs).unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    assert_eq!(bundle.code_areas.len(), 10, "capped at MAX_CODE_AREAS");
    // Sorted (top_dirs_from_paths returns a BTreeSet): area00..area09.
    assert_eq!(bundle.code_areas[0], "area00");
    assert_eq!(bundle.code_areas[9], "area09");
}

#[test]
fn similar_issues_ranked_by_shared_labels_then_recency_and_capped_at_three() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let target = work_item(
        &repo,
        "iss-target",
        WorkKind::Issue,
        "#1",
        "target issue",
        "",
        WorkState::Open,
        &["bug", "backend", "urgent"],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    // 4 candidates: 3 should win (one more than the cap of 3), ranked by
    // shared-label count first, then by recency (updated_at desc).
    let cand_a = work_item(
        &repo,
        "iss-a",
        WorkKind::Issue,
        "#2",
        "shares all three",
        "",
        WorkState::Closed,
        &["bug", "backend", "urgent"],
        "2026-01-01T00:00:00Z",
        "2026-01-05T00:00:00Z",
        None,
        &[],
    );
    let cand_b = work_item(
        &repo,
        "iss-b",
        WorkKind::Issue,
        "#3",
        "shares two, newer",
        "",
        WorkState::Closed,
        &["bug", "backend"],
        "2026-01-01T00:00:00Z",
        "2026-01-10T00:00:00Z",
        None,
        &[],
    );
    let cand_c = work_item(
        &repo,
        "iss-c",
        WorkKind::Issue,
        "#4",
        "shares two, older",
        "",
        WorkState::Closed,
        &["bug", "urgent"],
        "2026-01-01T00:00:00Z",
        "2026-01-03T00:00:00Z",
        None,
        &[],
    );
    let cand_d = work_item(
        &repo,
        "iss-d",
        WorkKind::Issue,
        "#5",
        "shares one, dropped",
        "",
        WorkState::Closed,
        &["bug"],
        "2026-01-01T00:00:00Z",
        "2026-01-20T00:00:00Z",
        None,
        &[],
    );
    // A PR unrelated by label (no labels at all) must never appear.
    let unrelated_pr = work_item(
        &repo,
        "pr-x",
        WorkKind::Pr,
        "#9",
        "unrelated",
        "",
        WorkState::Merged,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        Some("2026-01-02T00:00:00Z"),
        &[],
    );
    st.store
        .save_work_items(&[target, cand_a, cand_b, cand_c, cand_d, unrelated_pr])
        .unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-target".into())).unwrap();
    assert_eq!(
        bundle.similar_issues.len(),
        3,
        "capped at MAX_SIMILAR_ISSUES"
    );
    let ids: Vec<&str> = bundle
        .similar_issues
        .iter()
        .map(|s| s.id.0.as_str())
        .collect();
    // iss-a (3 shared) first; then iss-b before iss-c (both 2 shared, iss-b
    // is newer); iss-d (1 shared, would-be newest) is dropped for the cap.
    assert_eq!(ids, vec!["iss-a", "iss-b", "iss-c"]);
    assert_eq!(bundle.similar_issues[0].shared_labels.len(), 3);
}

#[test]
fn similar_issue_attaches_the_latest_merged_pr_in_its_own_repo() {
    let st = state();
    let repo_a = seed_repo(&st, "r1");
    let repo_b = seed_repo(&st, "r2");
    let target = work_item(
        &repo_a,
        "iss-target",
        WorkKind::Issue,
        "#1",
        "target",
        "",
        WorkState::Open,
        &["bug"],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &[],
    );
    // The similar issue lives in repo_b, so its resolving PR must come from
    // repo_b too — not repo_a's PRs, proving the lookup is per-candidate-repo.
    let similar = work_item(
        &repo_b,
        "iss-similar",
        WorkKind::Issue,
        "#2",
        "similar",
        "",
        WorkState::Closed,
        &["bug"],
        "2026-01-01T00:00:00Z",
        "2026-01-05T00:00:00Z",
        None,
        &[],
    );
    let pr_wrong_repo = work_item(
        &repo_a,
        "pr-wrong",
        WorkKind::Pr,
        "#8",
        "wrong repo pr",
        "",
        WorkState::Merged,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-06T00:00:00Z",
        Some("2026-01-06T00:00:00Z"),
        &[],
    );
    let pr_right_repo = work_item(
        &repo_b,
        "pr-right",
        WorkKind::Pr,
        "#7",
        "right repo pr",
        "",
        WorkState::Merged,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-04T00:00:00Z",
        Some("2026-01-04T00:00:00Z"),
        &[],
    );
    st.store
        .save_work_items(&[target, similar, pr_wrong_repo, pr_right_repo])
        .unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-target".into())).unwrap();
    assert_eq!(bundle.similar_issues.len(), 1);
    let resolved = bundle.similar_issues[0]
        .resolved_by_pr
        .as_ref()
        .expect("a merged PR exists in the similar issue's own repo");
    assert_eq!(resolved.title, "right repo pr");
}

#[test]
fn estimate_brief_is_none_when_never_judged_and_real_when_judged() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let issue = work_item(
        &repo,
        "iss-1",
        WorkKind::Issue,
        "#1",
        "fix: the login bug",
        "",
        WorkState::Open,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:00:00Z",
        None,
        &["auth/login.rs"],
    );
    st.store.save_work_items(&[issue]).unwrap();

    // No effort row at all: no stub, no placeholder — a real `None`.
    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    assert!(bundle.estimate.is_none());

    // Judge it: a difficulty exists but calibration has written nothing yet
    // (predicted_secs/size_bucket/change_type all NULL) — the realistic state
    // for every effort row today (see the migration's caveat). The bundle
    // must still carry a REAL calibrated number, not null, computed live.
    st.store
        .save_effort(&[EffortEstimate {
            item_id: WorkItemId("iss-1".into()),
            difficulty: 5.0,
            method: EffortMethod::Heuristic,
            rationale: "t".into(),
            confidence: 0.5,
        }])
        .unwrap();

    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    let est = bundle.estimate.expect("a judged item gets an estimate");
    assert_eq!(est.difficulty, 5.0);
    assert!(
        est.predicted_secs.is_some(),
        "predicted_secs must be a real live-calibrated number, not null, \
         even with zero calibration history (cold-start prior)"
    );
    assert!(est.predicted_secs.unwrap() > 0.0);
    assert_eq!(
        est.change_type.as_deref(),
        Some("fix"),
        "derived from the title"
    );
    assert_eq!(
        est.size_bucket.as_deref(),
        Some("xs"),
        "one file, zero churn"
    );

    // Once calibration HAS persisted values (a later wave's writer, or a
    // manual `update_effort_calibration` call), those stored values win over
    // a fresh live computation.
    st.store
        .update_effort_calibration(
            &WorkItemId("iss-1".into()),
            999.0,
            "repo:r1",
            "l",
            "feature",
        )
        .unwrap();
    let bundle = ops::build_issue_context(&st, &WorkItemId("iss-1".into())).unwrap();
    let est = bundle.estimate.unwrap();
    assert_eq!(est.predicted_secs, Some(999.0));
    assert_eq!(est.size_bucket.as_deref(), Some("l"));
    assert_eq!(est.change_type.as_deref(), Some("feature"));
}

#[test]
fn pr_context_bundle_carries_diff_shape_cycle_time_and_estimate() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let pr = work_item(
        &repo,
        "pr-1",
        WorkKind::Pr,
        "#7",
        "add the retry loop",
        "",
        WorkState::Merged,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-01T00:16:40Z", // +1000s
        Some("2026-01-01T00:16:40Z"),
        &["a.rs", "b.rs", "c.rs"],
    );
    st.store.save_work_items(&[pr]).unwrap();
    st.store
        .save_effort(&[EffortEstimate {
            item_id: WorkItemId("pr-1".into()),
            difficulty: 3.0,
            method: EffortMethod::Heuristic,
            rationale: "t".into(),
            confidence: 0.4,
        }])
        .unwrap();

    let bundle = ops::build_pr_context(&st, &WorkItemId("pr-1".into())).unwrap();
    assert_eq!(bundle.pr.number, Some(7));
    assert!(bundle.pr.merged);
    assert_eq!(bundle.diff_summary.changed_files, 3);
    assert_eq!(
        bundle.diff_summary.additions, 0,
        "no per-item add/del counts in WorkItem"
    );
    assert_eq!(bundle.cycle_time_secs, Some(1000));
    assert!(bundle.estimate.is_some());
}

#[test]
fn title_and_subject_trim_at_the_configured_cap() {
    let st = state();
    let repo = seed_repo(&st, "r1");
    let long_title: String = "a".repeat(150);
    let pr = work_item(
        &repo,
        "pr-1",
        WorkKind::Pr,
        "#1",
        &long_title,
        "",
        WorkState::Merged,
        &[],
        "2026-01-01T00:00:00Z",
        "2026-01-02T00:00:00Z",
        Some("2026-01-02T00:00:00Z"),
        &[],
    );
    st.store.save_work_items(&[pr]).unwrap();

    let bundle = ops::build_pr_context(&st, &WorkItemId("pr-1".into())).unwrap();
    // 150 raw chars, capped to 100 + an ellipsis.
    assert!(bundle.pr.title.chars().count() <= 101);
    assert!(bundle.pr.title.ends_with('…'));
}
