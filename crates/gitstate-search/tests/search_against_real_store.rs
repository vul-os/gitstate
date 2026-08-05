//! End-to-end fidelity check: seeds a real `SqliteStore` (the same one
//! `gitstate-daemon`/`gitstate-cli` use) with issues/PRs/commits, then drives
//! `gitstate_search::search`/`embed_pending` against it. This is the proof
//! that the pure ranking logic (`rrf`/`fuzzy`/`embed`) composes correctly
//! with the real FTS5 SQL and the real BLOB-encoded embedding column — none
//! of which the pure-function unit tests in `embed.rs`/`fuzzy.rs`/`rrf.rs`
//! exercise. Same rationale as `gitstate-calibrate`'s
//! `recompute_against_real_store.rs`.
//!
//! Each test exercises one of the three ranking paths in isolation, proving
//! the "semantic search fails quietly" risk this wave was warned about is
//! actually covered: a **known-correct** top result, not just "it returned
//! something".

use gitstate_core::{
    Forge, Repo, RepoId, SearchKind, Store, WorkItem, WorkItemId, WorkKind, WorkState,
};
use gitstate_search::{embed_pending, search};
use gitstate_store::SqliteStore;

fn seed_repo(store: &SqliteStore) -> RepoId {
    let repo = Repo {
        id: RepoId::new(),
        slug: "acme/widgets".into(),
        path: "/tmp/widgets".into(),
        remote_url: None,
        forge: Forge::GitHub,
        default_branch: "main".into(),
        last_scanned_at: None,
        added_at: "2026-01-01T00:00:00Z".into(),
    };
    store.upsert_repo(&repo).unwrap();
    repo.id
}

fn issue(repo: &RepoId, ext_ref: &str, title: &str, body: &str) -> WorkItem {
    WorkItem {
        id: WorkItemId::new(),
        repo_id: repo.clone(),
        kind: WorkKind::Issue,
        external_ref: ext_ref.into(),
        title: title.into(),
        body: body.into(),
        state: WorkState::Open,
        author_login: None,
        labels: vec![],
        created_at: "2026-01-01T00:00:00Z".into(),
        updated_at: "2026-01-01T00:00:00Z".into(),
        merged_at: None,
        closed_at: None,
        files_touched: vec![],
    }
}

/// FTS alone finds an exact keyword match — no embeddings needed at all
/// (mirrors Go's pre-vector behaviour: `semantic` is false until something
/// is actually embedded).
#[test]
fn fts_finds_an_exact_keyword_match_before_anything_is_embedded() {
    let store = SqliteStore::open_in_memory().unwrap();
    let repo = seed_repo(&store);
    let auth_issue = issue(
        &repo,
        "#1",
        "Fix authentication redirect loop",
        "Users cannot log in; the login flow keeps redirecting.",
    );
    let billing_issue = issue(
        &repo,
        "#2",
        "Update billing invoice export",
        "Switch the export format to CSV.",
    );
    store
        .save_work_items(&[auth_issue.clone(), billing_issue])
        .unwrap();

    let outcome = search(&store, "authentication", &[SearchKind::Issue], 20).unwrap();
    assert!(!outcome.semantic, "no issue is embedded yet");
    assert!(!outcome.fuzzy, "FTS matched directly");
    assert_eq!(outcome.results.len(), 1);
    assert_eq!(outcome.results[0].id, auth_issue.id.0);
    assert_eq!(outcome.results[0].title, "Fix authentication redirect loop");
}

/// The vector-KNN path finds a hit FTS structurally cannot: a query with a
/// typo that changes its stemmed token entirely, so FTS5's exact
/// (post-porter-stemming) token match returns nothing, while the embedder's
/// character-trigram robustness still ranks the typo'd issue on top.
/// This is the "semantic search fails quietly, prove a known-correct
/// ordering" check the wave asked for, run against the REAL store (not a
/// synthetic vector fixture) so the BLOB round-trip is on the hook too.
#[test]
fn vector_knn_finds_a_typo_query_fts_cannot_match_after_embedding() {
    let store = SqliteStore::open_in_memory().unwrap();
    let repo = seed_repo(&store);
    let auth_issue = issue(
        &repo,
        "#1",
        "Fix authentication redirect loop",
        "Users cannot log in; the login flow keeps redirecting.",
    );
    let billing_issue = issue(
        &repo,
        "#2",
        "Update billing invoice export",
        "Switch the export format to CSV.",
    );
    store
        .save_work_items(&[auth_issue.clone(), billing_issue.clone()])
        .unwrap();

    let embedded = embed_pending(&store, 100).unwrap();
    assert_eq!(embedded, 2, "both issues get embedded");
    // Idempotent: a second pass with nothing changed embeds nothing more.
    assert_eq!(embed_pending(&store, 100).unwrap(), 0);

    // "authentcation" (missing the 'i') never appears in either issue's
    // text, so FTS5's MATCH (AND-of-exact-stemmed-tokens) returns nothing.
    let query = "authentcation redirect loop";
    let fts_only = store.search_fts(&[SearchKind::Issue], query, 20).unwrap();
    assert!(
        fts_only.is_empty(),
        "FTS must not fuzzy-match a typo'd token"
    );

    let outcome = search(&store, query, &[SearchKind::Issue], 20).unwrap();
    assert!(outcome.semantic, "the vector ranker must have contributed");
    assert!(!outcome.fuzzy);
    assert!(!outcome.results.is_empty());
    assert_eq!(
        outcome.results[0].id, auth_issue.id.0,
        "the auth issue must outrank the unrelated billing issue"
    );
    assert_eq!(
        outcome.results[0].title, "Fix authentication redirect loop",
        "a vector-only hit must be hydrated with its real title, not left blank"
    );
    assert_ne!(
        outcome.results[0].id, billing_issue.id.0,
        "the unrelated issue must not rank first"
    );
}

/// Neither FTS nor the vector ranker has anything to say (nothing is
/// embedded), so the fuzzy trigram fallback is what finds a misspelled
/// query — the spike's actual deliverable.
#[test]
fn fuzzy_fallback_finds_a_misspelled_query_when_nothing_else_matches() {
    let store = SqliteStore::open_in_memory().unwrap();
    let repo = seed_repo(&store);
    let auth_issue = issue(
        &repo,
        "#1",
        "Fix authentication redirect loop",
        "Users cannot log in.",
    );
    store
        .save_work_items(std::slice::from_ref(&auth_issue))
        .unwrap();
    // Deliberately never call embed_pending — the vector path must contribute nothing.

    let query = "atuhentication redirct"; // two typos
    let outcome = search(&store, query, &[SearchKind::Issue], 20).unwrap();
    assert!(!outcome.semantic, "nothing was embedded");
    assert!(outcome.fuzzy, "must fall back to trigram matching");
    assert_eq!(outcome.results.len(), 1);
    assert_eq!(outcome.results[0].id, auth_issue.id.0);
}

/// A query matching nothing at all — no FTS token overlap, no embedded
/// issue to rank (so the vector path contributes zero hits, not just weak
/// ones — see the note below on why nothing is embedded here), and below
/// the fuzzy trigram floor — is an empty (not erroring) result.
///
/// Deliberately does NOT call `embed_pending` first: once *anything* is
/// embedded, brute-force vector KNN (faithfully mirroring Go's own
/// `SearchIssuesByVector`, which has no similarity floor) returns the top
/// `limit` embedded issues by similarity regardless of how weak that
/// similarity is, so `semantic` would be `true` and results non-empty even
/// for a wildly unrelated query. That is a real, correct, and NOT a fuzzy
/// property of both the Go reference and this port — proven directly in
/// `vector_knn_finds_a_typo_query_fts_cannot_match_after_embedding` above —
/// so the genuinely-empty case is tested here with an empty corpus of
/// embeddings instead.
#[test]
fn no_match_anywhere_is_an_empty_result_not_an_error() {
    let store = SqliteStore::open_in_memory().unwrap();
    let repo = seed_repo(&store);
    store
        .save_work_items(&[issue(&repo, "#1", "Fix authentication", "details")])
        .unwrap();

    let outcome = search(
        &store,
        "completely unrelated zephyr",
        &[SearchKind::Issue],
        20,
    )
    .unwrap();
    assert!(outcome.results.is_empty());
    assert!(!outcome.fuzzy);
    assert!(!outcome.semantic);
}

/// A blank/whitespace-only query is an empty result, not an error — mirrors
/// Go's `Search`'s own trimmed-empty-query short-circuit.
#[test]
fn blank_query_is_empty_not_an_error() {
    let store = SqliteStore::open_in_memory().unwrap();
    let outcome = search(&store, "   ", &[], 20).unwrap();
    assert!(outcome.results.is_empty());
    assert!(!outcome.fuzzy);
    assert!(!outcome.semantic);
}
