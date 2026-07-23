-- gitstate local schema (SQLite). Forward-only; applied at open().
-- Aggregates only — NO source is ever stored.

-- repos
CREATE TABLE repos (
  id TEXT PRIMARY KEY, slug TEXT NOT NULL, path TEXT NOT NULL,
  remote_url TEXT, forge TEXT NOT NULL,                 -- 'github'|'gitlab'|'local'
  default_branch TEXT NOT NULL DEFAULT 'main',
  last_scanned_at TEXT, added_at TEXT NOT NULL
);

-- contributors (merged identities)
CREATE TABLE contributors (
  id TEXT PRIMARY KEY, display_name TEXT NOT NULL, primary_email TEXT NOT NULL,
  emails TEXT NOT NULL DEFAULT '[]',                    -- JSON array
  login TEXT, is_agent INTEGER NOT NULL DEFAULT 0, agent_kind TEXT
);
CREATE UNIQUE INDEX idx_contrib_email ON contributors(primary_email);

-- commit cache (aggregates only; NO source stored)
CREATE TABLE commits (
  sha TEXT NOT NULL, repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  author_email TEXT NOT NULL, author_name TEXT NOT NULL, committed_at TEXT NOT NULL,
  additions INTEGER NOT NULL, deletions INTEGER NOT NULL, files_changed INTEGER NOT NULL,
  is_merge INTEGER NOT NULL, is_test_touch INTEGER NOT NULL, summary TEXT NOT NULL,
  PRIMARY KEY (repo_id, sha)
);

-- forge work items
CREATE TABLE work_items (
  id TEXT PRIMARY KEY, repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  kind TEXT NOT NULL, external_ref TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
  state TEXT NOT NULL, author_login TEXT, labels TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, merged_at TEXT, closed_at TEXT,
  files_touched TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX idx_wi_ref ON work_items(repo_id, kind, external_ref);

-- derived project state (one row per repo)
CREATE TABLE project_state (
  repo_id TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
  head_sha TEXT NOT NULL, open_prs INTEGER, merged_prs INTEGER, draft_prs INTEGER,
  open_issues INTEGER, closed_issues INTEGER, in_progress INTEGER, done INTEGER,
  cycle_time_p50_hours REAL, cycle_time_p90_hours REAL, change_failure_rate REAL,
  computed_at TEXT NOT NULL, warnings TEXT NOT NULL DEFAULT '[]'
);

-- derived contributions (per repo x contributor x window)
CREATE TABLE contributions (
  repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  contributor_id TEXT NOT NULL, from_ts TEXT NOT NULL, to_ts TEXT NOT NULL,
  dim_shipped REAL, dim_review REAL, dim_effort REAL, dim_quality REAL, dim_ownership REAL, dim_durability REAL,
  raw_json TEXT NOT NULL, agent_pct REAL NOT NULL DEFAULT 0, composite REAL NOT NULL,
  PRIMARY KEY (repo_id, contributor_id, from_ts, to_ts)
);

-- effort estimates + classifications (per work item)
CREATE TABLE effort (
  item_id TEXT PRIMARY KEY, difficulty REAL NOT NULL, method TEXT NOT NULL,
  rationale TEXT NOT NULL, confidence REAL NOT NULL
);
CREATE TABLE classifications (
  item_id TEXT PRIMARY KEY, category_key TEXT NOT NULL, confidence REAL NOT NULL,
  method TEXT NOT NULL, rationale TEXT NOT NULL
);

-- CRDT: contexts
CREATE TABLE contexts (
  id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  deleted INTEGER NOT NULL DEFAULT 0, del_hlc TEXT           -- serialized Hlc
);
CREATE TABLE context_members (                              -- OR-Set: repos, tags, prs
  context_id TEXT NOT NULL REFERENCES contexts(id) ON DELETE CASCADE,
  member_kind TEXT NOT NULL,                                -- 'repo'|'tag'|'pr'
  member_key TEXT NOT NULL,                                 -- repo_id | tag | 'slug#number'
  note TEXT, add_hlc TEXT, remove_hlc TEXT,
  PRIMARY KEY (context_id, member_kind, member_key)
);
CREATE TABLE context_field_clocks (                         -- per-field LWW clocks
  context_id TEXT NOT NULL, field TEXT NOT NULL, hlc TEXT NOT NULL,
  PRIMARY KEY (context_id, field)
);

-- CRDT: categories
CREATE TABLE categories (
  id TEXT PRIMARY KEY, key TEXT NOT NULL, label TEXT NOT NULL,
  parent_key TEXT, color TEXT, source TEXT NOT NULL,        -- 'taxonomy'|'local'|'peer'
  taxonomy_version TEXT, hlc TEXT NOT NULL, deleted INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_cat_key ON categories(key);
CREATE TABLE category_field_clocks (
  category_id TEXT NOT NULL, field TEXT NOT NULL, hlc TEXT NOT NULL,
  PRIMARY KEY (category_id, field)
);

-- CRDT op log (source of truth for sync)
CREATE TABLE sync_ops (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  op_json TEXT NOT NULL, hlc TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_sync_hlc ON sync_ops(hlc);

-- personalization feedback (local learning)
CREATE TABLE classify_feedback (
  id INTEGER PRIMARY KEY AUTOINCREMENT, item_id TEXT NOT NULL,
  category_key TEXT NOT NULL, created_at TEXT NOT NULL
);

-- generic kv (settings, peer id, weights, taxonomy pin)
CREATE TABLE kv ( k TEXT PRIMARY KEY, v TEXT NOT NULL );

-- NOTE: schema_migrations is created by the embedded migration runner
-- (migrations.rs) with IF NOT EXISTS before any migration runs, so it is
-- intentionally NOT declared here to avoid a duplicate-table error.
