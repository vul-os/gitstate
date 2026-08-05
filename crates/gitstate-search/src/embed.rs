//! A real, deterministic, dependency-free local text embedder for gitstate's
//! semantic issue search. Ported near-verbatim from Go's `internal/embed`
//! (`embed.go`) — the plan's claim that this file is "already stdlib-only,
//! ports to Rust `std` almost mechanically" checked out: FNV-1a hashing,
//! natural-log term weighting, and L2 normalization are all `std`/pure-math,
//! no ML crate of any kind is needed.
//!
//! ## Why local + deterministic (same rationale as the Go doc)
//! - No external service, no network, no API key — works offline.
//! - Deterministic: the same text always produces the same vector, so
//!   re-embedding is idempotent and tests are stable.
//!
//! The classic "hashing trick": text is tokenised into word unigrams +
//! character 3-grams, each feature is signed-hashed into one of [`DIM`]
//! buckets, accumulated with a log-scaled term-frequency weight, and the
//! result is L2-normalised. Character 3-grams give typo/morphology
//! robustness ("authentication" and "authenticate" share most trigrams);
//! word tokens anchor exact terms.
//!
//! ## What's different from Go
//! - Go binds a vector as a `pgvector` text literal (`ToPGVector`) cast
//!   `::vector` in SQL. SQLite has no vector column type, so this port
//!   stores the raw little-endian f32 bytes instead ([`to_bytes`]/
//!   [`from_bytes`]) in a BLOB column (`crates/gitstate-store`'s
//!   `work_item_embeddings.vector`) — see that migration's doc. Round-trip
//!   losslessness is tested below (bytes in, bytes out, byte-for-byte).
//! - Go's `counts` is a `map[string]int`, whose Go `range` iteration order is
//!   randomized per process. Two features that hash into the *same* bucket
//!   would then be summed in a different order on different runs — floating
//!   addition is not perfectly associative, so Go's own "the same text always
//!   produces the same vector" claim relies on that never actually mattering
//!   for realistic inputs (collisions are rare at 256 buckets for short
//!   text). This port uses a `BTreeMap` instead of a hash map so accumulation
//!   order is deterministic **by construction**, not by accident — a
//!   stated, deliberate hardening, not a behaviour change in the values any
//!   test here observes.

use std::collections::BTreeMap;

/// The embedding dimension. Must match `work_item_embeddings.vector`'s
/// expected byte length (`DIM * 4`, one little-endian f32 per component).
pub const DIM: usize = 256;

/// The stable model identifier persisted alongside every vector, so a future
/// re-embed can detect a model change. Same string as Go's `localModelID` —
/// this is the same algorithm, just a different implementation language.
pub const MODEL_ID: &str = "local-hash-256";

/// Embeds `text` into a [`DIM`]-length, L2-normalised vector. Empty text (or
/// text with no alphanumeric tokens — whitespace/punctuation-only) yields the
/// zero vector, which is safe to store: cosine similarity against a zero
/// vector is defined as 0 by [`cosine`], never a divide-by-zero panic.
pub fn embed(text: &str) -> Vec<f32> {
    let mut vec = vec![0f32; DIM];

    let tokens = tokenize(text);
    if tokens.is_empty() {
        return vec;
    }

    // Raw term frequencies per feature, so a log-TF weight (1 + ln(tf)) can
    // dampen very repetitive text without dropping the signal entirely.
    let mut counts: BTreeMap<String, u32> = BTreeMap::new();
    for tok in &tokens {
        *counts.entry(format!("w:{tok}")).or_insert(0) += 1; // word unigram
        for tg in char_trigrams(tok) {
            *counts.entry(format!("c:{tg}")).or_insert(0) += 1; // char 3-gram
        }
    }

    for (feat, tf) in &counts {
        let (bucket, sign) = hash_feature(feat);
        let weight = 1.0 + (*tf as f64).ln();
        vec[bucket] += sign * weight as f32;
    }

    l2_normalize(&mut vec);
    vec
}

/// Lowercases `text` and splits it into alphanumeric word tokens — anything
/// that is not a letter or digit is a separator. Mirrors Go's
/// `strings.FieldsFunc` + `unicode.IsLetter`/`IsDigit`.
fn tokenize(text: &str) -> Vec<String> {
    text.to_lowercase()
        .split(|c: char| !(c.is_alphanumeric()))
        .filter(|s| !s.is_empty())
        .map(str::to_string)
        .collect()
}

/// Character 3-grams of one token, padded with a leading/trailing space so
/// prefixes/suffixes are represented. A token too short to have a full
/// 3-rune window after padding still yields the whole padded string as one
/// gram (mirrors Go's `charTrigrams`). Operates on `char`s (Unicode scalar
/// values), matching Go's rune-based safety.
fn char_trigrams(tok: &str) -> Vec<String> {
    let padded = format!(" {tok} ");
    let runes: Vec<char> = padded.chars().collect();
    if runes.len() < 3 {
        return vec![padded];
    }
    (0..=runes.len() - 3)
        .map(|i| runes[i..i + 3].iter().collect())
        .collect()
}

/// Maps a feature string to a `(bucket, sign)` pair using FNV-1a (32-bit).
/// The sign bit comes from an independent high bit of the hash so
/// collisions are as likely to cancel as reinforce — the standard signed
/// hashing trick that keeps the embedding approximately unbiased. Mirrors
/// Go's `hashFeature` exactly (same FNV-1a constants, same bit/modulo choice).
fn hash_feature(feat: &str) -> (usize, f32) {
    const FNV_OFFSET_BASIS: u32 = 0x811c9dc5;
    const FNV_PRIME: u32 = 0x0100_0193;
    let mut h = FNV_OFFSET_BASIS;
    for b in feat.as_bytes() {
        h ^= *b as u32;
        h = h.wrapping_mul(FNV_PRIME);
    }
    let bucket = (h % DIM as u32) as usize;
    let sign = if h & 0x8000_0000 != 0 { -1.0 } else { 1.0 };
    (bucket, sign)
}

/// Scales `vec` in place to unit L2 norm. A zero vector is left unchanged
/// (no division by zero) — mirrors Go's `l2Normalize`.
fn l2_normalize(vec: &mut [f32]) {
    let sum_sq: f64 = vec.iter().map(|v| (*v as f64) * (*v as f64)).sum();
    if sum_sq == 0.0 {
        return;
    }
    let norm = sum_sq.sqrt() as f32;
    for v in vec.iter_mut() {
        *v /= norm;
    }
}

/// Serializes a vector to little-endian f32 bytes for the `BLOB` column —
/// SQLite has no vector column type, so this replaces Go's `ToPGVector`
/// (a `pgvector` text literal). `DIM * 4` bytes for a full-length vector.
pub fn to_bytes(v: &[f32]) -> Vec<u8> {
    let mut out = Vec::with_capacity(v.len() * 4);
    for f in v {
        out.extend_from_slice(&f.to_le_bytes());
    }
    out
}

/// The inverse of [`to_bytes`]. A trailing partial (non-multiple-of-4) tail
/// is dropped rather than panicking — defensive against a hand-edited or
/// truncated row, not expected in practice since every writer goes through
/// [`to_bytes`].
pub fn from_bytes(b: &[u8]) -> Vec<f32> {
    b.chunks_exact(4)
        .map(|c| f32::from_le_bytes([c[0], c[1], c[2], c[3]]))
        .collect()
}

/// Cosine similarity of two equal-length vectors. For already-L2-normalised
/// vectors (as produced by [`embed`]) this is just the dot product. Returns
/// 0 when either vector is the zero vector or the lengths differ. Mirrors
/// Go's `Cosine` exactly (f64 accumulation from f32 components).
pub fn cosine(a: &[f32], b: &[f32]) -> f64 {
    if a.len() != b.len() {
        return 0.0;
    }
    let mut dot = 0.0f64;
    let mut na = 0.0f64;
    let mut nb = 0.0f64;
    for i in 0..a.len() {
        let (x, y) = (a[i] as f64, b[i] as f64);
        dot += x * y;
        na += x * x;
        nb += y * y;
    }
    if na == 0.0 || nb == 0.0 {
        return 0.0;
    }
    dot / (na.sqrt() * nb.sqrt())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Ported from Go's `TestEmbedDeterministic`: the same text always
    /// yields the identical vector.
    #[test]
    fn embed_is_deterministic() {
        let text = "Fix authentication redirect loop on login";
        let a = embed(text);
        let b = embed(text);
        assert_eq!(a.len(), DIM);
        assert_eq!(b.len(), DIM);
        assert_eq!(a, b);
    }

    /// Ported from Go's `TestEmbedNormalized`.
    #[test]
    fn embed_is_l2_normalized_and_empty_text_is_zero_vector() {
        let v = embed("semantic search over issues");
        let norm: f64 = v
            .iter()
            .map(|f| (*f as f64) * (*f as f64))
            .sum::<f64>()
            .sqrt();
        assert!(
            (norm - 1.0).abs() < 1e-5,
            "expected unit L2 norm, got {norm}"
        );

        let z = embed("");
        assert_eq!(z.len(), DIM);
        assert!(z.iter().all(|f| *f == 0.0));

        // Whitespace/punctuation-only is also zero (no tokens).
        assert!(embed("   ... !!! ").iter().all(|f| *f == 0.0));
    }

    /// Ported from Go's `TestEmbedNearDuplicateCosine`: this is the property
    /// semantic search actually relies on, tested with fixed strings and a
    /// known-correct ordering (near-dup > unrelated), not a re-derived
    /// expectation.
    #[test]
    fn near_duplicate_text_is_more_similar_than_unrelated_text() {
        let base = "users cannot log in, the authentication flow is broken";
        let near_dup = "users can not login; the authentication flow seems broken";
        let unrelated = "update the billing invoice export to CSV format";

        let sim_dup = cosine(&embed(base), &embed(near_dup));
        let sim_unrel = cosine(&embed(base), &embed(unrelated));

        assert!(
            sim_dup > sim_unrel,
            "near-duplicate cosine ({sim_dup:.4}) should exceed unrelated cosine ({sim_unrel:.4})"
        );
        assert!(
            sim_dup > 0.0,
            "expected positive similarity for near-duplicate text"
        );

        let identical = cosine(&embed(base), &embed(base));
        assert!(
            (identical - 1.0).abs() < 1e-5,
            "identical text cosine should be ~1, got {identical:.6}"
        );
    }

    /// Ported from Go's `TestEmbedTypoRobust`.
    #[test]
    fn a_single_typo_stays_closer_than_an_unrelated_word() {
        let good = embed("authentication");
        let typo = embed("authentcation"); // missing an 'i'
        let cross = embed("deployment pipeline");
        assert!(
            cosine(&good, &typo) > cosine(&good, &cross),
            "typo variant should be closer than an unrelated word"
        );
    }

    /// `to_bytes`/`from_bytes` round-trip losslessly — the wave's explicit
    /// storage-format requirement. Exact float32 equality (not "close to"):
    /// the bytes are copied verbatim, no lossy re-encoding happens.
    #[test]
    fn to_bytes_from_bytes_roundtrips_losslessly() {
        let v = embed("a reasonably long piece of issue text with several words in it");
        let bytes = to_bytes(&v);
        assert_eq!(bytes.len(), DIM * 4);
        let back = from_bytes(&bytes);
        assert_eq!(back, v, "byte round-trip must be exact, not approximate");

        // Also check a hand-picked vector with negative/fractional values
        // and a couple of edges (0.0, -0.0's bit pattern is irrelevant here).
        let hand = vec![0.5f32, -0.25, 0.0, 1.0, -1.0, 123.456];
        let back2 = from_bytes(&to_bytes(&hand));
        assert_eq!(back2, hand);
    }

    #[test]
    fn model_id_is_stable() {
        assert_eq!(MODEL_ID, "local-hash-256");
    }

    /// Fixed-vector cosine sanity checks with hand-computed expected values —
    /// the "known-correct ordering" the wave asked for, independent of the
    /// embedder itself.
    #[test]
    fn cosine_matches_hand_computed_values() {
        assert_eq!(cosine(&[1.0, 0.0], &[1.0, 0.0]), 1.0);
        assert_eq!(cosine(&[1.0, 0.0], &[0.0, 1.0]), 0.0);
        assert_eq!(cosine(&[1.0, 0.0], &[-1.0, 0.0]), -1.0);
        assert_eq!(
            cosine(&[0.0, 0.0], &[1.0, 0.0]),
            0.0,
            "zero vector => 0, not NaN"
        );
        assert_eq!(
            cosine(&[1.0, 0.0], &[1.0, 0.0, 0.0]),
            0.0,
            "mismatched lengths => 0"
        );
    }
}
