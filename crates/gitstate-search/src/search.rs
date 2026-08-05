//! The search orchestrator: normalizes the request, runs FTS5 + vector KNN
//! and fuses them (RRF), hydrates any vector-only placeholder rows, and
//! falls back to the fuzzy trigram matcher when nothing else matched.
//! Mirrors Go's `store.Search` (`store/search.go`), reading through
//! [`Store`] the same way `gitstate_calibrate::recompute::recompute_calibration`
//! orchestrates over `Store` rather than touching SQL directly.

use gitstate_core::{parse_ref_number, Result, SearchHit, SearchKind, Store, WorkItemId};

use crate::embed;
use crate::fuzzy;
use crate::rrf::{self, VectorHit};

/// Mirrors Go's `searchDefaultLimit`.
pub const DEFAULT_LIMIT: u32 = 20;
/// Mirrors Go's `searchMaxLimit`.
pub const MAX_LIMIT: u32 = 100;
/// Mirrors Go's `embedBatchLimit`.
pub const EMBED_BATCH_LIMIT: u32 = 1000;

/// The trigram-similarity floor for the fuzzy fallback's issue branch,
/// matching Go's `simFloor` in `searchFuzzy`.
const FUZZY_ISSUE_FLOOR: f64 = 0.2;
/// The floor for PRs/commits, matching Go's `word_similarity(...) > 0.4`
/// threshold — same number, applied to `trigram_similarity` instead (see
/// `crate::fuzzy`'s doc for why that is a stated behaviour difference).
const FUZZY_PR_COMMIT_FLOOR: f64 = 0.4;

/// The result of one `search` call. Mirrors Go's `(results, fuzzy,
/// semantic, error)` return shape.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct SearchOutcome {
    pub results: Vec<SearchHit>,
    /// `true` when the fuzzy fallback produced the hits (FTS and vector both
    /// came up empty).
    pub fuzzy: bool,
    /// `true` when the vector ranker actually contributed a hit (i.e. at
    /// least one issue is embedded and matched the query).
    pub semantic: bool,
}

/// Resolves the requested type filter to the canonical, de-duplicated,
/// stably-ordered kind list. Empty input means "every kind" — mirrors Go's
/// `normalizeSearchTypes`' "empty or all-unknown => all three types" rule
/// (unknown-string handling itself lives in `SearchKind::parse`, called by
/// whichever caller — CLI/MCP — accepts raw strings).
pub fn normalize_kinds(kinds: &[SearchKind]) -> Vec<SearchKind> {
    const CANONICAL: [SearchKind; 3] = [SearchKind::Issue, SearchKind::Pr, SearchKind::Commit];
    if kinds.is_empty() {
        return CANONICAL.to_vec();
    }
    CANONICAL
        .into_iter()
        .filter(|c| kinds.contains(c))
        .collect()
}

/// Clamps a requested result-count limit: `0` becomes [`DEFAULT_LIMIT`], and
/// anything past [`MAX_LIMIT`] is capped. Mirrors Go's `clampLimit`.
pub fn clamp_limit(limit: u32) -> u32 {
    if limit == 0 {
        DEFAULT_LIMIT
    } else {
        limit.min(MAX_LIMIT)
    }
}

/// Runs hybrid search across the requested entity types: full-text (FTS5),
/// vector KNN over embedded issues (fused with FTS via [`rrf::fuse_hybrid`]),
/// and — only when both of those come up empty — the fuzzy trigram fallback.
/// Mirrors Go's `Search` end to end, including its short-circuit on a blank
/// query (empty result, not an error).
pub fn search(
    store: &dyn Store,
    query: &str,
    kinds: &[SearchKind],
    limit: u32,
) -> Result<SearchOutcome> {
    let query = query.trim();
    if query.is_empty() {
        return Ok(SearchOutcome::default());
    }
    let wanted = normalize_kinds(kinds);
    let limit = clamp_limit(limit);

    let fts_hits = store.search_fts(&wanted, query, limit)?;

    // Vector KNN over issues only, and only when issues are in scope — mirrors
    // Go's `wantsIssues` guard. Brute-force cosine over every embedded issue:
    // no ANN/HNSW index, correct at this app's local, single-user scale
    // (`docs/PORT-PLAN.md` §2/§5).
    let mut vector_hits: Vec<VectorHit> = Vec::new();
    if wanted.contains(&SearchKind::Issue) {
        let q_vec = embed::embed(query);
        let mut scored: Vec<VectorHit> = store
            .list_issue_embeddings()?
            .into_iter()
            .map(|(id, bytes)| {
                let v = embed::from_bytes(&bytes);
                VectorHit {
                    issue_id: id,
                    similarity: embed::cosine(&q_vec, &v),
                }
            })
            .collect();
        scored.sort_by(|a, b| b.similarity.partial_cmp(&a.similarity).unwrap());
        scored.truncate(limit as usize);
        vector_hits = scored;
    }

    let (fused, semantic) = rrf::fuse_hybrid(&fts_hits, &vector_hits, limit as usize);
    if !fused.is_empty() {
        let mut results = fused;
        if semantic {
            hydrate_missing_issues(store, &mut results)?;
        }
        return Ok(SearchOutcome {
            results,
            fuzzy: false,
            semantic,
        });
    }

    // Nothing matched FTS or vectors — typo-tolerant fuzzy fallback, exactly
    // as Go falls back when its FTS + vector pass both come up empty.
    let fuzzy_hits = fuzzy_fallback(store, &wanted, query, limit as usize)?;
    let found = !fuzzy_hits.is_empty();
    Ok(SearchOutcome {
        results: fuzzy_hits,
        fuzzy: found,
        semantic: false,
    })
}

/// Fills in the real fields for any issue-typed result whose row came ONLY
/// from the vector ranker (`title` is the placeholder empty string —
/// see `rrf::fuse_hybrid`'s doc). Reuses [`Store::get_work_item`] (already
/// added in wave 3 for `context_bundle`) rather than a dedicated batched
/// hydration query the way Go's `HydrateMissingIssues` was — one extra
/// `get_work_item` call per vector-only hit, which is negligible at this
/// app's local scale (at most `limit`, i.e. ≤100, extra point reads).
fn hydrate_missing_issues(store: &dyn Store, results: &mut [SearchHit]) -> Result<()> {
    for r in results.iter_mut() {
        if r.kind == SearchKind::Issue && r.title.is_empty() {
            if let Some(w) = store.get_work_item(&WorkItemId(r.id.clone()))? {
                r.number = parse_ref_number(&w.external_ref);
                r.title = w.title.clone();
                r.snippet = make_snippet(&w.title, &w.body);
                r.repo_id = w.repo_id.0.clone();
                r.state = w.state.as_str().to_string();
            }
        }
    }
    Ok(())
}

/// `left(title || ' ' || body, 160)`, trimmed — mirrors Go's snippet
/// construction in `HydrateMissingIssues`/`searchFuzzy`. Counts **characters**,
/// not bytes (Go's `left()` is a Postgres function operating on characters
/// too, so this is a faithful match, not the `ops.rs` "deliberate departure"
/// wave 3 noted for its own byte-slicing Go source).
fn make_snippet(title: &str, body: &str) -> String {
    let combined = format!("{title} {body}");
    let combined = combined.trim();
    combined.chars().take(160).collect()
}

/// Typo-tolerant fallback: issues are scored by [`fuzzy::trigram_similarity`]
/// on title alone (floor [`FUZZY_ISSUE_FLOOR`]); PRs and commits accept
/// either a case-insensitive substring match or a trigram-similarity floor
/// of [`FUZZY_PR_COMMIT_FLOOR`] — mirrors Go's `searchFuzzy` OR-condition
/// exactly, modulo the `word_similarity` → `trigram_similarity` swap
/// documented in `crate::fuzzy`. Reads through `list_all_work_items`/
/// `list_commits` (both already existed, added for other domains) rather
/// than a dedicated query — a genuine reuse, not new store surface.
fn fuzzy_fallback(
    store: &dyn Store,
    wanted: &[SearchKind],
    query: &str,
    limit: usize,
) -> Result<Vec<SearchHit>> {
    let mut candidates: Vec<SearchHit> = Vec::new();

    if wanted.contains(&SearchKind::Issue) || wanted.contains(&SearchKind::Pr) {
        for w in store.list_all_work_items()? {
            let kind = match w.kind {
                gitstate_core::WorkKind::Issue if wanted.contains(&SearchKind::Issue) => {
                    SearchKind::Issue
                }
                gitstate_core::WorkKind::Pr if wanted.contains(&SearchKind::Pr) => SearchKind::Pr,
                _ => continue,
            };
            let sim = fuzzy::trigram_similarity(&w.title, query);
            let passes = match kind {
                SearchKind::Issue => sim > FUZZY_ISSUE_FLOOR,
                SearchKind::Pr => {
                    sim > FUZZY_PR_COMMIT_FLOOR || fuzzy::contains_ci(&w.title, query)
                }
                SearchKind::Commit => unreachable!("commits are handled in the loop below"),
            };
            if !passes {
                continue;
            }
            candidates.push(SearchHit {
                kind,
                id: w.id.0.clone(),
                number: parse_ref_number(&w.external_ref),
                title: w.title.clone(),
                snippet: make_snippet(&w.title, ""),
                rank: sim,
                repo_id: w.repo_id.0.clone(),
                state: w.state.as_str().to_string(),
            });
        }
    }

    if wanted.contains(&SearchKind::Commit) {
        for c in store.list_commits(None)? {
            let sim = fuzzy::trigram_similarity(&c.summary, query);
            if sim > FUZZY_PR_COMMIT_FLOOR || fuzzy::contains_ci(&c.summary, query) {
                candidates.push(SearchHit {
                    kind: SearchKind::Commit,
                    id: c.sha.clone(),
                    number: None,
                    title: c.summary.clone(),
                    snippet: make_snippet(&c.summary, ""),
                    rank: sim,
                    repo_id: c.repo_id.0.clone(),
                    state: String::new(),
                });
            }
        }
    }

    // Stable rank-desc sort, ties broken by number desc — mirrors Go's
    // `ORDER BY rank DESC, number DESC`.
    candidates.sort_by(|a, b| {
        b.rank
            .partial_cmp(&a.rank)
            .unwrap()
            .then_with(|| b.number.cmp(&a.number))
    });
    candidates.truncate(limit);
    Ok(candidates)
}

/// Embeds every issue whose vector is missing or stale and persists the
/// result. Mirrors Go's `embed.EmbedPendingIssues` (`internal/embed/batch.go`):
/// idempotent (only pending rows are touched), and a per-item failure is
/// non-fatal (skipped, not propagated) so one bad row cannot abort the whole
/// batch. Returns the number of issues (re)embedded.
pub fn embed_pending(store: &dyn Store, limit: u32) -> Result<u64> {
    let limit = if limit == 0 { EMBED_BATCH_LIMIT } else { limit };
    let pending = store.list_issues_needing_embedding(embed::MODEL_ID, limit)?;
    let mut embedded = 0u64;
    for item in &pending {
        let vector = embed::embed(&format!("{}\n{}", item.title, item.body));
        let bytes = embed::to_bytes(&vector);
        if store
            .set_work_item_embedding(&item.id, &bytes, embed::MODEL_ID)
            .is_ok()
        {
            embedded += 1;
        }
    }
    Ok(embedded)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_kinds_empty_means_every_kind_in_canonical_order() {
        assert_eq!(
            normalize_kinds(&[]),
            vec![SearchKind::Issue, SearchKind::Pr, SearchKind::Commit]
        );
    }

    #[test]
    fn normalize_kinds_dedupes_and_reorders_to_canonical() {
        let got = normalize_kinds(&[SearchKind::Commit, SearchKind::Issue, SearchKind::Commit]);
        assert_eq!(got, vec![SearchKind::Issue, SearchKind::Commit]);
    }

    #[test]
    fn clamp_limit_defaults_and_caps() {
        assert_eq!(clamp_limit(0), DEFAULT_LIMIT);
        assert_eq!(clamp_limit(5), 5);
        assert_eq!(clamp_limit(9999), MAX_LIMIT);
    }

    #[test]
    fn make_snippet_trims_and_caps_at_160_chars() {
        let long_body = "x".repeat(200);
        let snip = make_snippet("title", &long_body);
        assert_eq!(snip.chars().count(), 160);

        // title's 2 trailing spaces + the literal separator + body's 2
        // leading spaces = 5 spaces between the trimmed words.
        assert_eq!(
            make_snippet("  hello  ", "  world  "),
            format!("hello{}world", " ".repeat(5))
        );
    }
}
