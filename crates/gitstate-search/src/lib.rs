//! gitstate-search — local hybrid search over issues, PRs, and commits.
//!
//! Ported from Go's `internal/embed` (`embed.go` + `batch.go`) and
//! `store/search.go` + `store/embeddings.go` (see `docs/PORT-PLAN.md` §2/§5,
//! wave 4). Three modules, matching the Go source's own shape:
//!
//! - [`embed`] — the dependency-free local text embedder (hashing trick:
//!   word unigrams + character 3-grams, FNV-1a signed hashing, log-TF
//!   weighting, L2 normalization). Pure, no I/O.
//! - [`fuzzy`] — the typo-tolerant trigram-similarity fallback that stands
//!   in for Postgres's `pg_trgm` extension, which SQLite has no equivalent
//!   of. **Read this module's doc for the wave's flagged spike, resolved.**
//!   Pure, no I/O.
//! - [`rrf`] — Reciprocal Rank Fusion of the FTS5 and vector-KNN issue
//!   rankings. Pure, no I/O.
//! - [`search`] — the orchestrator ([`search::search`]/[`search::embed_pending`])
//!   that calls [`gitstate_core::Store`]'s new wave-4 methods
//!   (`search_fts`/`list_issue_embeddings`/`list_issues_needing_embedding`/
//!   `set_work_item_embedding`) and the pure modules above, the same
//!   "orchestrator over `Store`, math stays pure" split
//!   `gitstate_calibrate::recompute` already established.
//!
//! ## The spike, resolved (summary — full writeup in [`fuzzy`]'s doc)
//!
//! SQLite has **FTS5** (confirmed available with **zero Cargo.toml change**:
//! `rusqlite` 0.32 has no `fts5` Cargo feature at all — the port plan's
//! suggestion of one was wrong — but `libsqlite3-sys`'s `bundled` build
//! already compiles SQLite with `-DSQLITE_ENABLE_FTS5` unconditionally). It
//! has **no `pg_trgm`, no trigram function, and no `spellfix1`/`editdist3`**
//! wired into this build. The fuzzy fallback is therefore a hand-rolled
//! trigram-Jaccard function (`fuzzy::trigram_similarity`), in the same
//! dependency-free spirit as the embedder itself — not a new crate
//! (`strsim` was the plan's other named option; declined, see `fuzzy`'s
//! doc for why). It replicates pg_trgm's symmetric `similarity()` formula
//! exactly but does **not** replicate the asymmetric `word_similarity()`
//! Go used for PR titles and commit messages — a stated, real ranking
//! difference for long targets, not a rounding difference. Full detail in
//! [`fuzzy`]'s module doc.

pub mod embed;
pub mod fuzzy;
pub mod rrf;
pub mod search;

pub use search::{clamp_limit, embed_pending, normalize_kinds, search, SearchOutcome};
