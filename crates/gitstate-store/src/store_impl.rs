//! `SqliteStore`: the local, single-file persistence layer.
//!
//! Non-CRDT tables are plain upserts. Contexts and categories are CRDT-backed:
//! a local edit mints a fresh [`Hlc`], writes the desired state through the
//! per-field LWW clocks / OR-Set member rows, and appends the equivalent
//! [`SyncOp`]s to the `sync_ops` log so peers can converge. Remote merges apply
//! the same op algebra from `gitstate-sync`.

use crate::migrations;
use crate::schema::*;
use gitstate_core::{
    ids::now_rfc3339, CatField, Category, CategorySource, Classification, Commit, Context,
    ContextPrRef, Contribution, Contributor, CtxField, DimensionRaw, Dimensions, EffortEstimate,
    EffortMethod, Error, Forge, Hlc, PeerId, ProjectState, Repo, Result, Store, SyncOp, WorkItem,
    WorkKind, WorkState,
};
use gitstate_core::{CategoryId, ContextId, ContributorId, RepoId, WorkItemId};
use rusqlite::{params, Connection, OptionalExtension};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

/// A raw `context_members` row: (member_kind, member_key, note, add_hlc, remove_hlc).
type MemberRow = (String, String, Option<String>, Option<String>, Option<String>);

/// The local SQLite-backed [`Store`].
pub struct SqliteStore {
    conn: Mutex<Connection>,
}

impl SqliteStore {
    /// Open (creating if needed) the DB at `db_path` and run migrations.
    pub fn open(db_path: &Path) -> Result<Self> {
        if let Some(parent) = db_path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let conn = Connection::open(db_path).map_err(st)?;
        Self::from_conn(conn)
    }

    /// In-memory DB (tests).
    pub fn open_in_memory() -> Result<Self> {
        let conn = Connection::open_in_memory().map_err(st)?;
        Self::from_conn(conn)
    }

    fn from_conn(conn: Connection) -> Result<Self> {
        let _mode: String = conn
            .query_row("PRAGMA journal_mode = WAL", [], |r| r.get(0))
            .map_err(st)?;
        conn.execute_batch("PRAGMA foreign_keys = ON;")
            .map_err(st)?;
        migrations::run(&conn)?;
        Ok(SqliteStore {
            conn: Mutex::new(conn),
        })
    }

    /// The per-OS data directory (env `GITSTATE_DATA_DIR` overrides).
    pub fn data_dir() -> Result<PathBuf> {
        if let Ok(dir) = std::env::var("GITSTATE_DATA_DIR") {
            if !dir.is_empty() {
                return Ok(PathBuf::from(dir));
            }
        }
        let base =
            dirs::data_dir().ok_or_else(|| Error::storage("no OS data directory available"))?;
        Ok(base.join("gitstate"))
    }
}

/// `<data>/gitstate.db`.
pub fn db_path(data_dir: &Path) -> PathBuf {
    data_dir.join("gitstate.db")
}

// ─────────────────────────── HLC / peer helpers ───────────────────────────

fn get_or_create_peer(conn: &Connection) -> Result<PeerId> {
    if let Some(v) = kv_get_conn(conn, "peer_id")? {
        return Ok(PeerId(v));
    }
    let p = PeerId::new();
    kv_set_conn(conn, "peer_id", &p.0)?;
    Ok(p)
}

/// Mint a strictly-increasing local clock.
fn next_hlc(conn: &Connection) -> Result<Hlc> {
    let peer = get_or_create_peer(conn)?;
    let wall = gitstate_core::now_wall_ms();
    let last = dec_hlc(kv_get_conn(conn, "hlc_last")?)?;
    let (w, c) = match last {
        Some(l) if l.wall_ms >= wall => (l.wall_ms, l.counter + 1),
        _ => (wall, 0),
    };
    let h = Hlc {
        wall_ms: w,
        counter: c,
        peer,
    };
    kv_set_conn(conn, "hlc_last", &h.encode())?;
    Ok(h)
}

fn kv_get_conn(conn: &Connection, k: &str) -> Result<Option<String>> {
    conn.query_row("SELECT v FROM kv WHERE k = ?1", [k], |r| {
        r.get::<_, String>(0)
    })
    .optional()
    .map_err(st)
}

fn kv_set_conn(conn: &Connection, k: &str, v: &str) -> Result<()> {
    conn.execute(
        "INSERT INTO kv (k, v) VALUES (?1, ?2)
         ON CONFLICT(k) DO UPDATE SET v = excluded.v",
        params![k, v],
    )
    .map_err(st)?;
    Ok(())
}

// ─────────────────────────── row mappers ───────────────────────────

fn map_repo(r: &rusqlite::Row) -> rusqlite::Result<Repo> {
    Ok(Repo {
        id: RepoId(r.get(0)?),
        slug: r.get(1)?,
        path: r.get(2)?,
        remote_url: r.get(3)?,
        forge: Forge::parse(&r.get::<_, String>(4)?).unwrap_or(Forge::Local),
        default_branch: r.get(5)?,
        last_scanned_at: r.get(6)?,
        added_at: r.get(7)?,
    })
}

const REPO_COLS: &str =
    "id, slug, path, remote_url, forge, default_branch, last_scanned_at, added_at";

fn map_contributor(r: &rusqlite::Row) -> rusqlite::Result<Contributor> {
    Ok(Contributor {
        id: ContributorId(r.get(0)?),
        display_name: r.get(1)?,
        primary_email: r.get(2)?,
        emails: parse_json(&r.get::<_, String>(3)?),
        login: r.get(4)?,
        is_agent: r.get::<_, i64>(5)? != 0,
        agent_kind: r.get(6)?,
    })
}

fn map_work_item(r: &rusqlite::Row) -> rusqlite::Result<WorkItem> {
    Ok(WorkItem {
        id: WorkItemId(r.get(0)?),
        repo_id: RepoId(r.get(1)?),
        kind: WorkKind::parse(&r.get::<_, String>(2)?).unwrap_or(WorkKind::Commit),
        external_ref: r.get(3)?,
        title: r.get(4)?,
        body: r.get(5)?,
        state: WorkState::parse(&r.get::<_, String>(6)?).unwrap_or(WorkState::Open),
        author_login: r.get(7)?,
        labels: parse_json(&r.get::<_, String>(8)?),
        created_at: r.get(9)?,
        updated_at: r.get(10)?,
        merged_at: r.get(11)?,
        closed_at: r.get(12)?,
        files_touched: parse_json(&r.get::<_, String>(13)?),
    })
}

const WI_COLS: &str = "id, repo_id, kind, external_ref, title, body, state, author_login, \
    labels, created_at, updated_at, merged_at, closed_at, files_touched";

// ─────────────────────────── CRDT: context ───────────────────────────

fn ensure_context_row(conn: &Connection, id: &ContextId, created_at: &str) -> Result<()> {
    let now = now_rfc3339();
    conn.execute(
        "INSERT OR IGNORE INTO contexts (id, name, description, notes, created_at, updated_at, deleted)
         VALUES (?1, '', '', '', ?2, ?3, 0)",
        params![id.0, if created_at.is_empty() { &now } else { created_at }, now],
    )
    .map_err(st)?;
    Ok(())
}

fn set_context_field_clock(
    conn: &Connection,
    id: &ContextId,
    field: &str,
    hlc: &Hlc,
) -> Result<()> {
    conn.execute(
        "INSERT INTO context_field_clocks (context_id, field, hlc) VALUES (?1, ?2, ?3)
         ON CONFLICT(context_id, field) DO UPDATE SET hlc = excluded.hlc",
        params![id.0, field, hlc.encode()],
    )
    .map_err(st)?;
    Ok(())
}

fn ops_for_context(old: Option<&Context>, new: &Context, hlc: &Hlc) -> Vec<SyncOp> {
    let mut ops = Vec::new();
    let empty = String::new();
    let old_name = old.map(|c| &c.name).unwrap_or(&empty);
    if old_name != &new.name {
        ops.push(SyncOp::ContextLww {
            id: new.id.clone(),
            field: CtxField::Name,
            value: new.name.clone(),
            hlc: hlc.clone(),
        });
    }
    let old_desc = old.map(|c| &c.description).unwrap_or(&empty);
    if old_desc != &new.description {
        ops.push(SyncOp::ContextLww {
            id: new.id.clone(),
            field: CtxField::Description,
            value: new.description.clone(),
            hlc: hlc.clone(),
        });
    }
    let old_notes = old.map(|c| &c.notes).unwrap_or(&empty);
    if old_notes != &new.notes {
        ops.push(SyncOp::ContextLww {
            id: new.id.clone(),
            field: CtxField::Notes,
            value: new.notes.clone(),
            hlc: hlc.clone(),
        });
    }

    let old_tags: Vec<String> = old.map(|c| c.tags.clone()).unwrap_or_default();
    for t in &new.tags {
        if !old_tags.contains(t) {
            ops.push(SyncOp::ContextTag {
                id: new.id.clone(),
                tag: t.clone(),
                add: true,
                hlc: hlc.clone(),
            });
        }
    }
    for t in &old_tags {
        if !new.tags.contains(t) {
            ops.push(SyncOp::ContextTag {
                id: new.id.clone(),
                tag: t.clone(),
                add: false,
                hlc: hlc.clone(),
            });
        }
    }

    let old_repos: Vec<RepoId> = old.map(|c| c.repo_ids.clone()).unwrap_or_default();
    for rid in &new.repo_ids {
        if !old_repos.iter().any(|x| x.0 == rid.0) {
            ops.push(SyncOp::ContextRepo {
                id: new.id.clone(),
                repo_id: rid.clone(),
                add: true,
                hlc: hlc.clone(),
            });
        }
    }
    for rid in &old_repos {
        if !new.repo_ids.iter().any(|x| x.0 == rid.0) {
            ops.push(SyncOp::ContextRepo {
                id: new.id.clone(),
                repo_id: rid.clone(),
                add: false,
                hlc: hlc.clone(),
            });
        }
    }

    let old_prs: Vec<ContextPrRef> = old.map(|c| c.pr_refs.clone()).unwrap_or_default();
    let ident = |p: &ContextPrRef| (p.repo_slug.clone(), p.number);
    for p in &new.pr_refs {
        if !old_prs.iter().any(|o| ident(o) == ident(p)) {
            ops.push(SyncOp::ContextPr {
                id: new.id.clone(),
                repo_slug: p.repo_slug.clone(),
                number: p.number,
                note: p.note.clone(),
                add: true,
                hlc: hlc.clone(),
            });
        }
    }
    for p in &old_prs {
        if !new.pr_refs.iter().any(|n| ident(n) == ident(p)) {
            ops.push(SyncOp::ContextPr {
                id: new.id.clone(),
                repo_slug: p.repo_slug.clone(),
                number: p.number,
                note: p.note.clone(),
                add: false,
                hlc: hlc.clone(),
            });
        }
    }

    let was_deleted = old.map(|c| c.deleted).unwrap_or(false);
    if new.deleted && !was_deleted {
        ops.push(SyncOp::ContextDel {
            id: new.id.clone(),
            hlc: hlc.clone(),
        });
    }
    ops
}

fn write_context(conn: &Connection, c: &Context, hlc: &Hlc) -> Result<()> {
    ensure_context_row(conn, &c.id, &c.created_at)?;
    let now = now_rfc3339();
    conn.execute(
        "UPDATE contexts SET name = ?2, description = ?3, notes = ?4, updated_at = ?5,
             deleted = ?6, del_hlc = ?7 WHERE id = ?1",
        params![
            c.id.0,
            c.name,
            c.description,
            c.notes,
            now,
            c.deleted as i64,
            if c.deleted {
                Some(hlc.encode())
            } else {
                None::<String>
            },
        ],
    )
    .map_err(st)?;
    set_context_field_clock(conn, &c.id, "name", hlc)?;
    set_context_field_clock(conn, &c.id, "description", hlc)?;
    set_context_field_clock(conn, &c.id, "notes", hlc)?;

    // Desired member set.
    let mut desired: Vec<(String, String, Option<String>)> = Vec::new(); // (kind, key, note)
    for rid in &c.repo_ids {
        desired.push(("repo".into(), rid.0.clone(), None));
    }
    for t in &c.tags {
        desired.push(("tag".into(), t.clone(), None));
    }
    for p in &c.pr_refs {
        desired.push((
            "pr".into(),
            format!("{}#{}", p.repo_slug, p.number),
            p.note.clone(),
        ));
    }

    // Tombstone existing members not in the desired set.
    let mut stmt = conn
        .prepare("SELECT member_kind, member_key FROM context_members WHERE context_id = ?1")
        .map_err(st)?;
    let existing: Vec<(String, String)> = stmt
        .query_map([&c.id.0], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))
        })
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(stmt);
    for (k, key) in &existing {
        if !desired.iter().any(|(dk, dkey, _)| dk == k && dkey == key) {
            conn.execute(
                "UPDATE context_members SET remove_hlc = ?3
                 WHERE context_id = ?1 AND member_kind = ?2 AND member_key = ?4",
                params![c.id.0, k, hlc.encode(), key],
            )
            .map_err(st)?;
        }
    }
    // Upsert present members as adds.
    for (kind, key, note) in &desired {
        conn.execute(
            "INSERT INTO context_members (context_id, member_kind, member_key, note, add_hlc, remove_hlc)
             VALUES (?1, ?2, ?3, ?4, ?5, NULL)
             ON CONFLICT(context_id, member_kind, member_key)
             DO UPDATE SET note = excluded.note, add_hlc = excluded.add_hlc, remove_hlc = NULL",
            params![c.id.0, kind, key, note, hlc.encode()],
        )
        .map_err(st)?;
    }
    Ok(())
}

fn reconstruct_context(conn: &Connection, id: &ContextId) -> Result<Option<Context>> {
    let row = conn
        .query_row(
            "SELECT name, description, notes, created_at, updated_at, deleted, del_hlc
             FROM contexts WHERE id = ?1",
            [&id.0],
            |r| {
                Ok((
                    r.get::<_, String>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, String>(3)?,
                    r.get::<_, String>(4)?,
                    r.get::<_, i64>(5)?,
                    r.get::<_, Option<String>>(6)?,
                ))
            },
        )
        .optional()
        .map_err(st)?;
    let Some((name, description, notes, created_at, updated_at, deleted, del_hlc_s)) = row else {
        return Ok(None);
    };

    let mut max_clock = zero_hlc();
    // field clocks
    let mut fc = conn
        .prepare("SELECT hlc FROM context_field_clocks WHERE context_id = ?1")
        .map_err(st)?;
    let clocks: Vec<String> = fc
        .query_map([&id.0], |r| r.get::<_, String>(0))
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(fc);
    for c in clocks {
        if let Ok(h) = Hlc::decode(&c) {
            if h > max_clock {
                max_clock = h;
            }
        }
    }

    let mut repo_ids = Vec::new();
    let mut tags = Vec::new();
    let mut pr_refs = Vec::new();
    let mut ms = conn
        .prepare(
            "SELECT member_kind, member_key, note, add_hlc, remove_hlc
             FROM context_members WHERE context_id = ?1",
        )
        .map_err(st)?;
    let members: Vec<MemberRow> = ms
        .query_map([&id.0], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, Option<String>>(2)?,
                r.get::<_, Option<String>>(3)?,
                r.get::<_, Option<String>>(4)?,
            ))
        })
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(ms);

    for (kind, key, note, add_s, rem_s) in members {
        let add = dec_hlc(add_s)?;
        let rem = dec_hlc(rem_s)?;
        if let Some(a) = &add {
            if a > &max_clock {
                max_clock = a.clone();
            }
        }
        if let Some(r) = &rem {
            if r > &max_clock {
                max_clock = r.clone();
            }
        }
        let present = match (&add, &rem) {
            (Some(a), Some(r)) => a >= r,
            (Some(_), None) => true,
            _ => false,
        };
        if !present {
            continue;
        }
        match kind.as_str() {
            "repo" => repo_ids.push(RepoId(key)),
            "tag" => tags.push(key),
            "pr" => {
                if let Some((slug, num)) = key.rsplit_once('#') {
                    if let Ok(number) = num.parse::<u64>() {
                        pr_refs.push(ContextPrRef {
                            repo_slug: slug.to_string(),
                            number,
                            note,
                        });
                    }
                }
            }
            _ => {}
        }
    }

    let del_hlc = dec_hlc(del_hlc_s)?;
    let hlc = match &del_hlc {
        Some(d) if d > &max_clock => d.clone(),
        _ => max_clock,
    };

    Ok(Some(Context {
        id: id.clone(),
        name,
        description,
        repo_ids,
        pr_refs,
        notes,
        tags,
        created_at,
        updated_at,
        hlc,
        deleted: deleted != 0,
    }))
}

// ─────────────────────────── CRDT: category ───────────────────────────

fn write_category(conn: &Connection, c: &Category, hlc: &Hlc) -> Result<()> {
    conn.execute(
        "INSERT INTO categories (id, key, label, parent_key, color, source, taxonomy_version, hlc, deleted)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)
         ON CONFLICT(id) DO UPDATE SET
            key = excluded.key, label = excluded.label, parent_key = excluded.parent_key,
            color = excluded.color, source = excluded.source,
            taxonomy_version = excluded.taxonomy_version, hlc = excluded.hlc,
            deleted = excluded.deleted",
        params![
            c.id.0,
            c.key,
            c.label,
            c.parent_key,
            c.color,
            c.source.as_str(),
            c.taxonomy_version,
            hlc.encode(),
            c.deleted as i64,
        ],
    )
    .map_err(st)?;
    for f in ["label", "color", "parent_key"] {
        conn.execute(
            "INSERT INTO category_field_clocks (category_id, field, hlc) VALUES (?1, ?2, ?3)
             ON CONFLICT(category_id, field) DO UPDATE SET hlc = excluded.hlc",
            params![c.id.0, f, hlc.encode()],
        )
        .map_err(st)?;
    }
    Ok(())
}

fn ops_for_category(old: Option<&Category>, new: &Category, hlc: &Hlc) -> Vec<SyncOp> {
    let mut ops = Vec::new();
    let old_label = old.map(|c| c.label.clone());
    if old_label.as_deref() != Some(new.label.as_str()) {
        ops.push(SyncOp::CategoryLww {
            id: new.id.clone(),
            key: new.key.clone(),
            field: CatField::Label,
            value: new.label.clone(),
            hlc: hlc.clone(),
        });
    }
    let old_color = old.and_then(|c| c.color.clone());
    if old_color != new.color {
        ops.push(SyncOp::CategoryLww {
            id: new.id.clone(),
            key: new.key.clone(),
            field: CatField::Color,
            value: new.color.clone().unwrap_or_default(),
            hlc: hlc.clone(),
        });
    }
    let old_parent = old.and_then(|c| c.parent_key.clone());
    if old_parent != new.parent_key {
        ops.push(SyncOp::CategoryLww {
            id: new.id.clone(),
            key: new.key.clone(),
            field: CatField::ParentKey,
            value: new.parent_key.clone().unwrap_or_default(),
            hlc: hlc.clone(),
        });
    }
    if new.deleted && !old.map(|c| c.deleted).unwrap_or(false) {
        ops.push(SyncOp::CategoryDel {
            id: new.id.clone(),
            hlc: hlc.clone(),
        });
    }
    ops
}

fn map_category(r: &rusqlite::Row) -> rusqlite::Result<Category> {
    let hlc_s: String = r.get(7)?;
    Ok(Category {
        id: CategoryId(r.get(0)?),
        key: r.get(1)?,
        label: r.get(2)?,
        parent_key: r.get(3)?,
        color: r.get(4)?,
        source: CategorySource::parse(&r.get::<_, String>(5)?).unwrap_or(CategorySource::Local),
        taxonomy_version: r.get(6)?,
        hlc: Hlc::decode(&hlc_s).unwrap_or_else(|_| zero_hlc()),
        deleted: r.get::<_, i64>(8)? != 0,
    })
}

const CAT_COLS: &str = "id, key, label, parent_key, color, source, taxonomy_version, hlc, deleted";

fn append_ops(conn: &Connection, ops: &[SyncOp]) -> Result<()> {
    for op in ops {
        let json = serde_json::to_string(op)?;
        conn.execute(
            "INSERT INTO sync_ops (op_json, hlc, applied) VALUES (?1, ?2, 1)",
            params![json, op.hlc().encode()],
        )
        .map_err(st)?;
    }
    Ok(())
}

// ─────────────────────────── Store impl ───────────────────────────

impl Store for SqliteStore {
    fn upsert_repo(&self, repo: &Repo) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO repos (id, slug, path, remote_url, forge, default_branch, last_scanned_at, added_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
             ON CONFLICT(id) DO UPDATE SET
                slug = excluded.slug, path = excluded.path, remote_url = excluded.remote_url,
                forge = excluded.forge, default_branch = excluded.default_branch,
                last_scanned_at = excluded.last_scanned_at",
            params![
                repo.id.0,
                repo.slug,
                repo.path,
                repo.remote_url,
                repo.forge.as_str(),
                repo.default_branch,
                repo.last_scanned_at,
                repo.added_at,
            ],
        )
        .map_err(st)?;
        Ok(())
    }

    fn get_repo(&self, id: &RepoId) -> Result<Option<Repo>> {
        let conn = self.conn.lock().unwrap();
        conn.query_row(
            &format!("SELECT {REPO_COLS} FROM repos WHERE id = ?1"),
            [&id.0],
            map_repo,
        )
        .optional()
        .map_err(st)
    }

    fn list_repos(&self) -> Result<Vec<Repo>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(&format!("SELECT {REPO_COLS} FROM repos ORDER BY added_at"))
            .map_err(st)?;
        let rows = stmt
            .query_map([], map_repo)
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn delete_repo(&self, id: &RepoId) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute("DELETE FROM repos WHERE id = ?1", [&id.0])
            .map_err(st)?;
        Ok(())
    }

    fn upsert_contributor(&self, c: &Contributor) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO contributors (id, display_name, primary_email, emails, login, is_agent, agent_kind)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
             ON CONFLICT(primary_email) DO UPDATE SET
                display_name = excluded.display_name, emails = excluded.emails,
                login = excluded.login, is_agent = excluded.is_agent, agent_kind = excluded.agent_kind",
            params![
                c.id.0,
                c.display_name,
                c.primary_email,
                json_str(&c.emails),
                c.login,
                c.is_agent as i64,
                c.agent_kind,
            ],
        )
        .map_err(st)?;
        Ok(())
    }

    fn list_contributors(&self) -> Result<Vec<Contributor>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(
                "SELECT id, display_name, primary_email, emails, login, is_agent, agent_kind
                 FROM contributors ORDER BY display_name",
            )
            .map_err(st)?;
        let rows = stmt
            .query_map([], map_contributor)
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn save_commits(&self, repo: &RepoId, commits: &[Commit]) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        for c in commits {
            tx.execute(
                "INSERT OR REPLACE INTO commits
                 (sha, repo_id, author_email, author_name, committed_at, additions, deletions,
                  files_changed, is_merge, is_test_touch, summary)
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)",
                params![
                    c.sha,
                    repo.0,
                    c.author_email,
                    c.author_name,
                    c.committed_at,
                    c.additions,
                    c.deletions,
                    c.files_changed,
                    c.is_merge as i64,
                    c.is_test_touch as i64,
                    c.summary,
                ],
            )
            .map_err(st)?;
        }
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn save_project_state(&self, s: &ProjectState) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT OR REPLACE INTO project_state
             (repo_id, head_sha, open_prs, merged_prs, draft_prs, open_issues, closed_issues,
              in_progress, done, cycle_time_p50_hours, cycle_time_p90_hours, change_failure_rate,
              computed_at, warnings)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14)",
            params![
                s.repo_id.0,
                s.head_sha,
                s.open_prs,
                s.merged_prs,
                s.draft_prs,
                s.open_issues,
                s.closed_issues,
                s.in_progress,
                s.done,
                s.cycle_time_p50_hours,
                s.cycle_time_p90_hours,
                s.change_failure_rate,
                s.computed_at,
                json_str(&s.warnings),
            ],
        )
        .map_err(st)?;
        Ok(())
    }

    fn get_project_state(&self, repo: &RepoId) -> Result<Option<ProjectState>> {
        let conn = self.conn.lock().unwrap();
        conn.query_row(
            "SELECT repo_id, head_sha, open_prs, merged_prs, draft_prs, open_issues, closed_issues,
                    in_progress, done, cycle_time_p50_hours, cycle_time_p90_hours,
                    change_failure_rate, computed_at, warnings
             FROM project_state WHERE repo_id = ?1",
            [&repo.0],
            |r| {
                Ok(ProjectState {
                    repo_id: RepoId(r.get(0)?),
                    head_sha: r.get(1)?,
                    open_prs: r.get(2)?,
                    merged_prs: r.get(3)?,
                    draft_prs: r.get(4)?,
                    open_issues: r.get(5)?,
                    closed_issues: r.get(6)?,
                    in_progress: r.get(7)?,
                    done: r.get(8)?,
                    cycle_time_p50_hours: r.get(9)?,
                    cycle_time_p90_hours: r.get(10)?,
                    change_failure_rate: r.get(11)?,
                    computed_at: r.get(12)?,
                    warnings: parse_json(&r.get::<_, String>(13)?),
                })
            },
        )
        .optional()
        .map_err(st)
    }

    fn save_contributions(&self, rows: &[Contribution]) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        for c in rows {
            tx.execute(
                "INSERT OR REPLACE INTO contributions
                 (repo_id, contributor_id, from_ts, to_ts, dim_shipped, dim_review, dim_effort,
                  dim_quality, dim_ownership, dim_durability, raw_json, agent_pct, composite)
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13)",
                params![
                    c.repo_id.0,
                    c.contributor_id.0,
                    c.from,
                    c.to,
                    c.dimensions.shipped,
                    c.dimensions.review,
                    c.dimensions.effort,
                    c.dimensions.quality,
                    c.dimensions.ownership,
                    c.dimensions.durability,
                    serde_json::to_string(&c.raw)?,
                    c.agent_pct,
                    c.composite,
                ],
            )
            .map_err(st)?;
        }
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn get_contributions(&self, repo: &RepoId, from: &str, to: &str) -> Result<Vec<Contribution>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(
                "SELECT repo_id, contributor_id, from_ts, to_ts, dim_shipped, dim_review,
                        dim_effort, dim_quality, dim_ownership, dim_durability, raw_json,
                        agent_pct, composite
                 FROM contributions WHERE repo_id = ?1 AND from_ts = ?2 AND to_ts = ?3",
            )
            .map_err(st)?;
        let rows = stmt
            .query_map(params![repo.0, from, to], |r| {
                let raw_json: String = r.get(10)?;
                Ok(Contribution {
                    repo_id: RepoId(r.get(0)?),
                    contributor_id: ContributorId(r.get(1)?),
                    from: r.get(2)?,
                    to: r.get(3)?,
                    dimensions: Dimensions {
                        shipped: r.get(4)?,
                        review: r.get(5)?,
                        effort: r.get(6)?,
                        quality: r.get(7)?,
                        ownership: r.get(8)?,
                        durability: r.get(9)?,
                    },
                    raw: serde_json::from_str(&raw_json).unwrap_or(DimensionRaw::default()),
                    agent_pct: r.get(11)?,
                    composite: r.get(12)?,
                })
            })
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn save_work_items(&self, items: &[WorkItem]) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        for w in items {
            tx.execute(
                "INSERT OR REPLACE INTO work_items
                 (id, repo_id, kind, external_ref, title, body, state, author_login, labels,
                  created_at, updated_at, merged_at, closed_at, files_touched)
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14)",
                params![
                    w.id.0,
                    w.repo_id.0,
                    w.kind.as_str(),
                    w.external_ref,
                    w.title,
                    w.body,
                    w.state.as_str(),
                    w.author_login,
                    json_str(&w.labels),
                    w.created_at,
                    w.updated_at,
                    w.merged_at,
                    w.closed_at,
                    json_str(&w.files_touched),
                ],
            )
            .map_err(st)?;
        }
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn list_work_items(&self, repo: &RepoId) -> Result<Vec<WorkItem>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {WI_COLS} FROM work_items WHERE repo_id = ?1 ORDER BY created_at DESC"
            ))
            .map_err(st)?;
        let rows = stmt
            .query_map([&repo.0], map_work_item)
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn save_effort(&self, rows: &[EffortEstimate]) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        for e in rows {
            tx.execute(
                "INSERT OR REPLACE INTO effort (item_id, difficulty, method, rationale, confidence)
                 VALUES (?1, ?2, ?3, ?4, ?5)",
                params![
                    e.item_id.0,
                    e.difficulty,
                    e.method.as_str(),
                    e.rationale,
                    e.confidence
                ],
            )
            .map_err(st)?;
        }
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn save_classifications(&self, rows: &[Classification]) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        for c in rows {
            tx.execute(
                "INSERT OR REPLACE INTO classifications
                 (item_id, category_key, confidence, method, rationale)
                 VALUES (?1, ?2, ?3, ?4, ?5)",
                params![
                    c.item_id.0,
                    c.category_key,
                    c.confidence,
                    c.method.as_str(),
                    c.rationale
                ],
            )
            .map_err(st)?;
        }
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn get_classification(&self, item: &WorkItemId) -> Result<Option<Classification>> {
        let conn = self.conn.lock().unwrap();
        conn.query_row(
            "SELECT item_id, category_key, confidence, method, rationale
             FROM classifications WHERE item_id = ?1",
            [&item.0],
            |r| {
                Ok(Classification {
                    item_id: WorkItemId(r.get(0)?),
                    category_key: r.get(1)?,
                    confidence: r.get(2)?,
                    method: EffortMethod::parse(&r.get::<_, String>(3)?)
                        .unwrap_or(EffortMethod::Heuristic),
                    rationale: r.get(4)?,
                })
            },
        )
        .optional()
        .map_err(st)
    }

    fn upsert_context(&self, c: &Context) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let hlc = next_hlc(&conn)?;
        let tx = conn.transaction().map_err(st)?;
        let old = reconstruct_context(&tx, &c.id)?;
        let ops = ops_for_context(old.as_ref(), c, &hlc);
        write_context(&tx, c, &hlc)?;
        append_ops(&tx, &ops)?;
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn get_context(&self, id: &ContextId) -> Result<Option<Context>> {
        let conn = self.conn.lock().unwrap();
        reconstruct_context(&conn, id)
    }

    fn list_contexts(&self) -> Result<Vec<Context>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare("SELECT id FROM contexts WHERE deleted = 0 ORDER BY created_at")
            .map_err(st)?;
        let ids: Vec<String> = stmt
            .query_map([], |r| r.get::<_, String>(0))
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        drop(stmt);
        let mut out = Vec::with_capacity(ids.len());
        for id in ids {
            if let Some(ctx) = reconstruct_context(&conn, &ContextId(id))? {
                if !ctx.deleted {
                    out.push(ctx);
                }
            }
        }
        Ok(out)
    }

    fn upsert_category(&self, c: &Category) -> Result<()> {
        let mut conn = self.conn.lock().unwrap();
        let hlc = next_hlc(&conn)?;
        let tx = conn.transaction().map_err(st)?;
        let old = tx
            .query_row(
                &format!("SELECT {CAT_COLS} FROM categories WHERE id = ?1"),
                [&c.id.0],
                map_category,
            )
            .optional()
            .map_err(st)?;
        let ops = ops_for_category(old.as_ref(), c, &hlc);
        write_category(&tx, c, &hlc)?;
        append_ops(&tx, &ops)?;
        tx.commit().map_err(st)?;
        Ok(())
    }

    fn get_category(&self, key: &str) -> Result<Option<Category>> {
        let conn = self.conn.lock().unwrap();
        conn.query_row(
            &format!("SELECT {CAT_COLS} FROM categories WHERE key = ?1"),
            [key],
            map_category,
        )
        .optional()
        .map_err(st)
    }

    fn list_categories(&self) -> Result<Vec<Category>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {CAT_COLS} FROM categories WHERE deleted = 0 ORDER BY key"
            ))
            .map_err(st)?;
        let rows = stmt
            .query_map([], map_category)
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn record_feedback(&self, item: &WorkItemId, chosen_key: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO classify_feedback (item_id, category_key, created_at)
             VALUES (?1, ?2, ?3)",
            params![item.0, chosen_key, now_rfc3339()],
        )
        .map_err(st)?;
        Ok(())
    }

    fn kv_get(&self, k: &str) -> Result<Option<String>> {
        let conn = self.conn.lock().unwrap();
        kv_get_conn(&conn, k)
    }

    fn kv_set(&self, k: &str, v: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        kv_set_conn(&conn, k, v)
    }

    fn append_sync_ops(&self, ops: &[SyncOp]) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        append_ops(&conn, ops)
    }

    fn sync_ops_since(&self, since: Option<&Hlc>) -> Result<Vec<SyncOp>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare("SELECT op_json, hlc FROM sync_ops ORDER BY seq")
            .map_err(st)?;
        let rows: Vec<(String, String)> = stmt
            .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        drop(stmt);
        let mut out = Vec::new();
        for (json, hlc_s) in rows {
            if let Some(floor) = since {
                if let Ok(h) = Hlc::decode(&hlc_s) {
                    if &h <= floor {
                        continue;
                    }
                }
            }
            if let Ok(op) = serde_json::from_str::<SyncOp>(&json) {
                out.push(op);
            }
        }
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{ContextPrRef, Weights};

    fn store() -> SqliteStore {
        SqliteStore::open_in_memory().unwrap()
    }

    #[test]
    fn repo_roundtrip() {
        let s = store();
        let repo = Repo {
            id: RepoId::new(),
            slug: "vul-os/gitstate".into(),
            path: "/tmp/x".into(),
            remote_url: Some("https://github.com/vul-os/gitstate".into()),
            forge: Forge::GitHub,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: now_rfc3339(),
        };
        s.upsert_repo(&repo).unwrap();
        let got = s.get_repo(&repo.id).unwrap().unwrap();
        assert_eq!(got.slug, "vul-os/gitstate");
        assert_eq!(got.forge, Forge::GitHub);
        assert_eq!(s.list_repos().unwrap().len(), 1);
    }

    #[test]
    fn context_crdt_roundtrip_and_ops() {
        let s = store();
        let ctx = Context {
            id: ContextId::new(),
            name: "Q3 refactor".into(),
            description: "cleanup".into(),
            repo_ids: vec![RepoId("r1".into())],
            pr_refs: vec![ContextPrRef {
                repo_slug: "vul-os/gitstate".into(),
                number: 42,
                note: Some("core".into()),
            }],
            notes: "notes".into(),
            tags: vec!["refactor".into()],
            created_at: now_rfc3339(),
            updated_at: now_rfc3339(),
            hlc: zero_hlc(),
            deleted: false,
        };
        s.upsert_context(&ctx).unwrap();
        let got = s.get_context(&ctx.id).unwrap().unwrap();
        assert_eq!(got.name, "Q3 refactor");
        assert_eq!(got.repo_ids.len(), 1);
        assert_eq!(got.pr_refs.len(), 1);
        assert_eq!(got.tags, vec!["refactor".to_string()]);
        assert_eq!(s.list_contexts().unwrap().len(), 1);

        // ops were logged for peers
        let ops = s.sync_ops_since(None).unwrap();
        assert!(!ops.is_empty());

        // remove a tag + delete
        let mut edited = got.clone();
        edited.tags.clear();
        s.upsert_context(&edited).unwrap();
        let got2 = s.get_context(&edited.id).unwrap().unwrap();
        assert!(got2.tags.is_empty());

        let mut del = got2.clone();
        del.deleted = true;
        s.upsert_context(&del).unwrap();
        assert_eq!(s.list_contexts().unwrap().len(), 0);
    }

    #[test]
    fn category_crdt_roundtrip() {
        let s = store();
        let cat = Category {
            id: CategoryId::new(),
            key: "feature.api".into(),
            label: "API feature".into(),
            parent_key: Some("feature".into()),
            color: Some("#4f46e5".into()),
            source: CategorySource::Local,
            taxonomy_version: None,
            hlc: zero_hlc(),
            deleted: false,
        };
        s.upsert_category(&cat).unwrap();
        let got = s.get_category("feature.api").unwrap().unwrap();
        assert_eq!(got.label, "API feature");
        assert_eq!(s.list_categories().unwrap().len(), 1);
    }

    #[test]
    fn weights_normalize() {
        let w = Weights::default_weights().normalized();
        let sum = w.shipped + w.review + w.effort + w.quality + w.ownership + w.durability;
        assert!((sum - 1.0).abs() < 1e-9);
    }
}
