//! The fuzzy-search fallback — this wave's flagged spike.
//!
//! ## The spike, resolved
//!
//! Go's fuzzy fallback (`store/search.go`'s `searchFuzzy`) leans on
//! Postgres's `pg_trgm` extension: `similarity()` (a symmetric trigram-set
//! Jaccard score, used for issue titles) and `word_similarity()` (an
//! asymmetric "does some substring of the longer string closely match the
//! shorter one" score, used for PR titles and commit messages, combined with
//! a plain `ILIKE '%query%'` OR-condition).
//!
//! **SQLite has no `pg_trgm` and no built-in trigram function of any kind.**
//! `crates/gitstate-store`'s FTS5 build (confirmed via `libsqlite3-sys`'s
//! `build.rs`, which passes `-DSQLITE_ENABLE_FTS5` unconditionally whenever
//! the already-on `bundled` feature compiles SQLite from source — see
//! migration `0005`'s doc) does *not* pull in `spellfix1` or any
//! `editdist3`-style fuzzy-matching extension either; those are separate,
//! non-default SQLite loadable extensions bundled by `libsqlite3-sys`'s
//! build under different feature flags this workspace does not enable, and
//! enabling them would be exactly the kind of "one more Cargo feature to
//! verify from scratch" risk the port plan flagged this whole domain for.
//!
//! The decision: **a hand-rolled trigram-Jaccard function**, in the same
//! "dependency-free, deterministic" spirit `internal/embed` was already
//! built in — no new crate (ruling out `strsim`, the plan's other named
//! option, deliberately: Levenshtein/Jaro-Winkler are edit-distance
//! algorithms with a *different* similarity shape than trigram-set overlap,
//! and pulling in a crate for one function when the existing embedder
//! already proves hand-rolled trigram logic is cheap and correct here would
//! be inconsistent with the rest of this domain).
//!
//! ## Precisely what behaviour differs from Postgres
//!
//! - [`trigram_similarity`] uses pg_trgm's own convention (2 leading blanks +
//!   1 trailing blank, then overlapping 3-char windows over the padded,
//!   lowercased string) and its exact formula, `|A ∩ B| / |A ∪ B|` over the
//!   **set** (not multiset) of trigrams — so scores are the same *shape* and
//!   qualitatively comparable to what Postgres would return, not a
//!   different algorithm wearing the same name.
//! - It does **not** implement `word_similarity()`. Go used `word_similarity`
//!   for PR titles and commit messages specifically because it is
//!   asymmetric: it finds the best-matching substring of the longer text
//!   for the shorter query, so a short query can score well against a long
//!   title/message even when most of that text is irrelevant. This port
//!   uses the ONE symmetric `similarity()`-equivalent for issues, PRs, and
//!   commits alike. **The real consequence**: a long PR title or commit
//!   subject that only partially echoes the query will rank *lower* here
//!   than it would have under Go's `word_similarity` — whole-string overlap
//!   is diluted by the rest of the text, where Go's asymmetric search would
//!   have ignored the irrelevant remainder. This is a genuine ranking
//!   difference for long targets, not a rounding difference.
//! - Go's fuzzy floor for issues was `similarity > 0.2`; for PRs/commits it
//!   was `ILIKE '%query%' OR word_similarity > 0.4`. This port keeps the
//!   0.2 floor for issues' `trigram_similarity` and, for PRs/commits, keeps
//!   the same OR-shape but swaps `word_similarity > 0.4` for
//!   `trigram_similarity > 0.4` — the threshold NUMBER is preserved even
//!   though the underlying function it gates is not the same function, so
//!   the practical effect is "somewhat stricter for long targets, unchanged
//!   for short ones" rather than a re-tuned threshold.

use std::collections::HashSet;

/// The trigram-set of `s`, using pg_trgm's own padding convention (two
/// leading blanks, one trailing blank) before extracting overlapping 3-char
/// windows over the lowercased, padded string. A `HashSet` — pg_trgm's
/// `similarity()` operates over the set of *distinct* trigrams, not a bag
/// with repeats.
fn trigrams(s: &str) -> HashSet<String> {
    let padded = format!("  {} ", s.to_lowercase());
    let chars: Vec<char> = padded.chars().collect();
    if chars.len() < 3 {
        return HashSet::from([padded]);
    }
    (0..=chars.len() - 3)
        .map(|i| chars[i..i + 3].iter().collect())
        .collect()
}

/// Trigram-Jaccard similarity in `[0, 1]`: `|shared trigrams| / |trigrams in
/// either string|`. gitstate's stand-in for Postgres's `pg_trgm::similarity`
/// — see the module doc for exactly how this differs from `word_similarity`,
/// which this function does **not** replicate. `0.0` if either string has
/// no trigrams (empty input).
pub fn trigram_similarity(a: &str, b: &str) -> f64 {
    let ta = trigrams(a);
    let tb = trigrams(b);
    if a.is_empty() || b.is_empty() {
        return 0.0;
    }
    let inter = ta.intersection(&tb).count();
    let union = ta.union(&tb).count();
    if union == 0 {
        0.0
    } else {
        inter as f64 / union as f64
    }
}

/// Case-insensitive substring containment — the Rust equivalent of Go's
/// `coalesce(title,'') ILIKE '%' || $1 || '%'` OR-branch for PR/commit fuzzy
/// matching. An empty `needle` never matches (mirrors `Search`'s own
/// trimmed-empty-query short-circuit — the fuzzy path is never reached with
/// one anyway, but this keeps the helper honest standalone).
pub fn contains_ci(haystack: &str, needle: &str) -> bool {
    if needle.is_empty() {
        return false;
    }
    haystack.to_lowercase().contains(&needle.to_lowercase())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Hand-computed, not re-derived: `"ab"` vs `"ac"` share exactly one
    /// padded trigram (`"  a"`) out of five distinct trigrams total, working
    /// the pg_trgm padding convention out by hand.
    ///
    /// `"ab"` → padded `"  ab "` → windows `{"  a", " ab", "ab "}`.
    /// `"ac"` → padded `"  ac "` → windows `{"  a", " ac", "ac "}`.
    /// intersection = `{"  a"}` (1), union = 5 ⇒ similarity = 0.2.
    #[test]
    fn trigram_similarity_matches_a_hand_computed_value() {
        assert_eq!(trigram_similarity("ab", "ac"), 0.2);
    }

    #[test]
    fn identical_strings_are_maximally_similar() {
        assert_eq!(trigram_similarity("authentication", "authentication"), 1.0);
    }

    #[test]
    fn empty_input_is_zero_not_a_panic() {
        assert_eq!(trigram_similarity("", "authentication"), 0.0);
        assert_eq!(trigram_similarity("authentication", ""), 0.0);
        assert_eq!(trigram_similarity("", ""), 0.0);
    }

    /// The known-correct ordering the wave asked for: a near-duplicate
    /// phrase must score higher than an unrelated one, and a single-typo
    /// variant must score higher than a wholly different word — the two
    /// properties the fuzzy fallback exists to provide.
    #[test]
    fn near_duplicates_and_typos_outrank_unrelated_text() {
        let base = "fix authentication redirect loop";
        let near_dup = "fix authentication redirect loops";
        let typo = "fix authentcation redirect loop"; // missing an 'i'
        let unrelated = "update billing invoice export";

        let sim_dup = trigram_similarity(base, near_dup);
        let sim_typo = trigram_similarity(base, typo);
        let sim_unrelated = trigram_similarity(base, unrelated);

        assert!(sim_dup > sim_unrelated);
        assert!(sim_typo > sim_unrelated);
        assert!(
            sim_dup > 0.5,
            "near-duplicate should score highly, got {sim_dup}"
        );
    }

    #[test]
    fn contains_ci_is_case_insensitive_and_rejects_empty_needle() {
        assert!(contains_ci("Fix Authentication Bug", "authentication"));
        assert!(contains_ci("Fix Authentication Bug", "AUTH"));
        assert!(!contains_ci("Fix Authentication Bug", "billing"));
        assert!(!contains_ci("anything", ""));
    }
}
