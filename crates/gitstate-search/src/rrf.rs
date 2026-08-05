//! Reciprocal Rank Fusion (RRF) of the FTS and vector-KNN issue rankings.
//! Ported near-verbatim from Go's `store/search.go`'s `fuseHybrid` — pure
//! function, no I/O, so it is unit-tested here with fixed fixtures and a
//! known-correct expected order rather than re-derived at call time (the
//! "semantic search fails quietly, bad ranking looks plausible" risk this
//! wave was warned about).

use gitstate_core::{SearchHit, SearchKind, WorkItemId};

/// The standard RRF constant (60), same as Go's `rrfK`. It damps the
/// contribution of low-ranked items so a result must place well in at least
/// one ranker to surface; a hit appearing in *both* rankings is rewarded by
/// the sum of its reciprocal ranks.
pub const RRF_K: f64 = 60.0;

/// One vector-KNN hit: an issue id and its cosine similarity to the query.
#[derive(Debug, Clone, PartialEq)]
pub struct VectorHit {
    pub issue_id: WorkItemId,
    pub similarity: f64,
}

/// Fuses `fts` (full-text hits, already ranked best-first) with `vector`
/// (vector-KNN issue hits, already ranked best-first by similarity) using
/// Reciprocal Rank Fusion, returning the top `limit` rows by fused score and
/// whether the vector ranker contributed at all (`semantic`).
///
/// A vector hit for an issue not present in `fts` is inserted as a
/// **placeholder row**: `title`/`snippet`/`repo_id`/`state` empty, `number`
/// `None`, `rank` set to the raw cosine similarity. The caller
/// (`gitstate_search::search`) is responsible for hydrating any placeholder
/// row's real fields (via `Store::get_work_item` — Go's Postgres version
/// used a dedicated batched hydration query; this port just reuses the
/// `get_work_item` method that already exists for `context_bundle`, wave 3).
///
/// `semantic` mirrors Go exactly: it is `true` whenever `vector` is
/// non-empty, regardless of whether a given hit was new or reinforced an
/// existing FTS row — Go's loop sets it unconditionally on every iteration,
/// so it reduces to "did the vector ranker run and return anything".
pub fn fuse_hybrid(
    fts: &[SearchHit],
    vector: &[VectorHit],
    limit: usize,
) -> (Vec<SearchHit>, bool) {
    struct FusedRow {
        res: SearchHit,
        score: f64,
    }

    // Key = (kind, id) so PRs/commits never collide with issues — mirrors
    // Go's `type + "\x00" + id` string key as a proper tuple instead.
    let mut order: Vec<(SearchKind, String)> = Vec::new();
    let mut rows: std::collections::HashMap<(SearchKind, String), FusedRow> =
        std::collections::HashMap::new();

    for (i, r) in fts.iter().enumerate() {
        let key = (r.kind, r.id.clone());
        rows.entry(key.clone()).or_insert_with(|| {
            order.push(key.clone());
            FusedRow {
                res: r.clone(),
                score: 0.0,
            }
        });
        rows.get_mut(&key).unwrap().score += 1.0 / (RRF_K + (i + 1) as f64);
    }

    let semantic = !vector.is_empty();
    for (i, vh) in vector.iter().enumerate() {
        let key = (SearchKind::Issue, vh.issue_id.0.clone());
        rows.entry(key.clone()).or_insert_with(|| {
            order.push(key.clone());
            FusedRow {
                res: SearchHit {
                    kind: SearchKind::Issue,
                    id: vh.issue_id.0.clone(),
                    number: None,
                    title: String::new(),
                    snippet: String::new(),
                    rank: vh.similarity,
                    repo_id: String::new(),
                    state: String::new(),
                },
                score: 0.0,
            }
        });
        rows.get_mut(&key).unwrap().score += 1.0 / (RRF_K + (i + 1) as f64);
    }

    // Stable sort by fused score descending — ties keep first-seen order,
    // matching Go's insertion-sort-based stable re-rank.
    let mut ordered: Vec<FusedRow> = order
        .into_iter()
        .map(|k| rows.remove(&k).expect("key was just inserted"))
        .collect();
    ordered.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap());

    let results = ordered.into_iter().take(limit).map(|fr| fr.res).collect();
    (results, semantic)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn hit(kind: SearchKind, id: &str, title: &str) -> SearchHit {
        SearchHit {
            kind,
            id: id.to_string(),
            number: None,
            title: title.to_string(),
            snippet: String::new(),
            rank: 0.0, // FTS's own rank value is irrelevant to fusion — only position is
            repo_id: "r1".to_string(),
            state: "open".to_string(),
        }
    }

    fn vhit(id: &str, sim: f64) -> VectorHit {
        VectorHit {
            issue_id: WorkItemId(id.to_string()),
            similarity: sim,
        }
    }

    /// Fixed inputs, hand-computed expected order — the exact scenario the
    /// wave asked to be tested this way. fts = [A, B, C] (ranks 1,2,3);
    /// vector = [B, D] (ranks 1,2 — D is vector-only, not in fts).
    ///
    /// RRF_K = 60. Scores:
    ///   A = 1/61              ≈ 0.0163934
    ///   B = 1/62 (fts) + 1/61 (vector) ≈ 0.0161290 + 0.0163934 = 0.0325224
    ///   C = 1/63              ≈ 0.0158730
    ///   D = 1/62 (vector-only) ≈ 0.0161290
    ///
    /// Expected order by score desc: B > A > D > C.
    #[test]
    fn fuses_fts_and_vector_rankings_by_hand_computed_rrf_score() {
        let fts = vec![
            hit(SearchKind::Issue, "A", "issue A"),
            hit(SearchKind::Issue, "B", "issue B"),
            hit(SearchKind::Issue, "C", "issue C"),
        ];
        let vector = vec![vhit("B", 0.9), vhit("D", 0.5)];

        let (fused, semantic) = fuse_hybrid(&fts, &vector, 10);
        assert!(semantic, "vector ranker contributed hits");

        let ids: Vec<&str> = fused.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(ids, vec!["B", "A", "D", "C"]);

        // The fused row for B carries the ORIGINAL fts row's real fields
        // (title "issue B"), not the vector placeholder — it was already
        // present in `fts`, so fusion must not overwrite it.
        let b = fused.iter().find(|r| r.id == "B").unwrap();
        assert_eq!(b.title, "issue B");

        // D is vector-only: title/snippet/repo_id/state are the placeholder
        // sentinel (empty) — the orchestrator is responsible for hydrating.
        let d = fused.iter().find(|r| r.id == "D").unwrap();
        assert_eq!(d.title, "");
        assert_eq!(d.repo_id, "");
        assert_eq!(d.rank, 0.5, "placeholder rank is the raw cosine similarity");
    }

    #[test]
    fn no_vector_hits_is_fts_only_and_not_semantic() {
        let fts = vec![
            hit(SearchKind::Issue, "A", "issue A"),
            hit(SearchKind::Pr, "P1", "pr P1"),
        ];
        let (fused, semantic) = fuse_hybrid(&fts, &[], 10);
        assert!(!semantic);
        assert_eq!(fused.len(), 2);
        assert_eq!(fused[0].id, "A");
        assert_eq!(fused[1].id, "P1");
    }

    #[test]
    fn limit_truncates_the_fused_result() {
        let fts: Vec<SearchHit> = (0..5)
            .map(|i| hit(SearchKind::Issue, &format!("i{i}"), "t"))
            .collect();
        let (fused, _) = fuse_hybrid(&fts, &[], 2);
        assert_eq!(fused.len(), 2);
        assert_eq!(fused[0].id, "i0");
        assert_eq!(fused[1].id, "i1");
    }

    #[test]
    fn a_pr_and_an_issue_sharing_the_same_id_string_do_not_collide() {
        // Same raw id, different kind — the (kind, id) composite key must
        // keep them as two distinct rows, not merge their scores.
        let fts = vec![
            hit(SearchKind::Issue, "42", "issue #42"),
            hit(SearchKind::Pr, "42", "pr #42"),
        ];
        let (fused, _) = fuse_hybrid(&fts, &[], 10);
        assert_eq!(fused.len(), 2);
    }

    #[test]
    fn empty_input_produces_empty_output() {
        let (fused, semantic) = fuse_hybrid(&[], &[], 10);
        assert!(fused.is_empty());
        assert!(!semantic);
    }
}
