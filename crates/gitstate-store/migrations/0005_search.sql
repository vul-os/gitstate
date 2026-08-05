-- Hybrid search (full-text + fuzzy + semantic) over issues, PRs, and commits.
--
-- Ported from Go's `store/search.go` + `store/embeddings.go` (T11 port plan,
-- wave 4). Two real departures from the Postgres design, both forced by
-- SQLite rather than invented here — see `crates/gitstate-search`'s crate doc
-- for the full spike writeup:
--
-- 1. No `pgvector` column type: an embedding is stored as a plain BLOB of
--    little-endian f32 bytes (`gitstate_search::embed::to_bytes`), not a
--    `::vector`-cast text literal. Brute-force cosine over this table is fast
--    enough at this app's local, single-user scale (hundreds to low
--    thousands of issues) — no ANN/HNSW index, matching `docs/PORT-PLAN.md`
--    §2/§5's reasoning.
-- 2. No `pg_trgm`: the fuzzy fallback is a hand-rolled trigram-Jaccard
--    function in `gitstate_search::fuzzy`, run in Rust over rows pulled
--    through the existing `list_all_work_items`/`list_commits` reads — no new
--    table needed for it.
--
-- `search_fts` is FTS5, NOT `pg_trgm`/`tsvector`: confirmed available with
-- ZERO Cargo.toml change (rusqlite has no `fts5` Cargo feature at all in
-- 0.32 — the plan's suggestion of one was wrong; `libsqlite3-sys`'s bundled
-- build passes `-DSQLITE_ENABLE_FTS5` unconditionally whenever the
-- already-enabled `bundled` feature builds SQLite from source). It is
-- rebuilt from scratch (`DELETE` + re-`INSERT`) on every `search_fts` call
-- rather than kept continuously in sync via triggers: `work_items`/`commits`
-- use TEXT primary keys, which FTS5 "external content" mode cannot address
-- directly (it wants an INTEGER `content_rowid`), and at local scale a full
-- rebuild costs low milliseconds — cheaper than building and maintaining a
-- rowid-alias/trigger scheme for no measurable benefit. This is a deliberate
-- departure from Postgres's continuously-maintained generated `search_tsv`
-- columns, not an oversight.
--
-- `org_id` is dropped, matching every prior wave (single tenant, nothing to
-- scope).
CREATE VIRTUAL TABLE search_fts USING fts5(
  entity_type UNINDEXED,   -- 'issue' | 'pr' | 'commit'
  entity_id UNINDEXED,     -- work_items.id, or commits.sha for a commit
  external_ref UNINDEXED,  -- "#123" / "!45" — parsed to a number by the caller
  repo_id UNINDEXED,
  state UNINDEXED,         -- '' for commits
  title,
  body,                    -- '' for PRs/commits — see gitstate-search's doc on
                           -- why PR/commit bodies are not indexed
  tokenize = 'porter unicode61'
);

-- Local semantic embeddings over issues only (mirrors Go's scope: PRs/commits
-- were never embedded there either). One row per embedded issue; missing row
-- = never embedded (the `search_fts`/fuzzy paths still cover it).
CREATE TABLE work_item_embeddings (
  item_id       TEXT PRIMARY KEY REFERENCES work_items(id) ON DELETE CASCADE,
  vector        BLOB NOT NULL,   -- Dim (256) little-endian f32s
  model         TEXT NOT NULL,
  embedded_at   TEXT NOT NULL
);
