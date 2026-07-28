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
    WorkKind, WorkState, HLC_SKEW_MS,
};
use gitstate_core::{CategoryId, ContextId, ContributorId, RepoId, WorkItemId};
use rusqlite::{params, Connection, OptionalExtension};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

/// A raw `context_members` row: (member_kind, member_key, note, add_hlc, remove_hlc).
type MemberRow = (
    String,
    String,
    Option<String>,
    Option<String>,
    Option<String>,
);

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

fn map_commit(r: &rusqlite::Row) -> rusqlite::Result<Commit> {
    Ok(Commit {
        sha: r.get(0)?,
        repo_id: RepoId(r.get(1)?),
        author_email: r.get(2)?,
        author_name: r.get(3)?,
        committed_at: r.get(4)?,
        additions: r.get(5)?,
        deletions: r.get(6)?,
        files_changed: r.get(7)?,
        is_merge: r.get::<_, i64>(8)? != 0,
        is_test_touch: r.get::<_, i64>(9)? != 0,
        summary: r.get(10)?,
    })
}

const COMMIT_COLS: &str = "sha, repo_id, author_email, author_name, committed_at, additions, \
    deletions, files_changed, is_merge, is_test_touch, summary";

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
        set_category_field_clock(conn, &c.id.0, f, hlc)?;
    }
    // A local delete must leave the same tombstone clock a remote `CategoryDel`
    // would, or a later merge would recompute `deleted` from the clock map and
    // resurrect the row. See `DEL_CLOCK`.
    if c.deleted {
        set_category_field_clock(conn, &c.id.0, DEL_CLOCK, hlc)?;
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

// ──────────────── CRDT: replaying a remote op into the rows ────────────────
//
// `append_sync_ops` records history; the functions below are what actually
// MOVE local state when a peer's op arrives. The merge rules they implement are
// the ones documented on `gitstate_sync::crdt`:
//
//   * scalar fields  — last-writer-wins by `Hlc`, per field, using the
//     `*_field_clocks` maps the schema already carries;
//   * set members    — add-wins OR-Set over `context_members.add_hlc` /
//     `remove_hlc`, exactly the pair `reconstruct_context` reads back;
//   * deletion       — a whole-document tombstone that a strictly later write
//     resurrects.
//
// Every rule is expressed as "take the max clock", so applying a batch is
// commutative (any arrival order lands on the same rows) and idempotent
// (re-applying an op changes nothing). That is what lets `sync_ops_since`
// return the log in local arrival order without breaking convergence.

/// The reserved key under which a *category's* tombstone clock lives in
/// `category_field_clocks`. `contexts` has a dedicated `del_hlc` column;
/// `categories` does not, and that table is already a generic per-document
/// clock map — so the tombstone clock goes in it under a name no `CatField`
/// can produce, which keeps both documents on one whole-doc LWW rule without
/// altering an applied migration.
const DEL_CLOCK: &str = "__del";

fn context_field_clock(conn: &Connection, id: &str, field: &str) -> Result<Option<Hlc>> {
    let s: Option<String> = conn
        .query_row(
            "SELECT hlc FROM context_field_clocks WHERE context_id = ?1 AND field = ?2",
            params![id, field],
            |r| r.get(0),
        )
        .optional()
        .map_err(st)?;
    dec_hlc(s)
}

fn category_field_clock(conn: &Connection, id: &str, field: &str) -> Result<Option<Hlc>> {
    let s: Option<String> = conn
        .query_row(
            "SELECT hlc FROM category_field_clocks WHERE category_id = ?1 AND field = ?2",
            params![id, field],
            |r| r.get(0),
        )
        .optional()
        .map_err(st)?;
    dec_hlc(s)
}

fn set_category_field_clock(conn: &Connection, id: &str, field: &str, hlc: &Hlc) -> Result<()> {
    conn.execute(
        "INSERT INTO category_field_clocks (category_id, field, hlc) VALUES (?1, ?2, ?3)
         ON CONFLICT(category_id, field) DO UPDATE SET hlc = excluded.hlc",
        params![id, field, hlc.encode()],
    )
    .map_err(st)?;
    Ok(())
}

/// The highest clock of any *write* recorded against a context — every field
/// clock plus every member add/remove clock. Deliberately excludes `del_hlc`:
/// this is the thing a tombstone is compared against.
fn context_write_clock(conn: &Connection, id: &str) -> Result<Hlc> {
    let mut max = zero_hlc();
    let mut push = |s: Option<String>| -> Result<()> {
        if let Some(h) = dec_hlc(s)? {
            if h > max {
                max = h;
            }
        }
        Ok(())
    };

    let mut stmt = conn
        .prepare("SELECT hlc FROM context_field_clocks WHERE context_id = ?1")
        .map_err(st)?;
    let field_clocks: Vec<String> = stmt
        .query_map([id], |r| r.get::<_, String>(0))
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(stmt);
    for c in field_clocks {
        push(Some(c))?;
    }

    let mut stmt = conn
        .prepare("SELECT add_hlc, remove_hlc FROM context_members WHERE context_id = ?1")
        .map_err(st)?;
    let member_clocks: Vec<(Option<String>, Option<String>)> = stmt
        .query_map([id], |r| Ok((r.get(0)?, r.get(1)?)))
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(stmt);
    for (a, r) in member_clocks {
        push(a)?;
        push(r)?;
    }
    Ok(max)
}

/// The highest clock of any category field write, excluding the tombstone.
fn category_write_clock(conn: &Connection, id: &str) -> Result<Hlc> {
    let mut stmt = conn
        .prepare("SELECT hlc FROM category_field_clocks WHERE category_id = ?1 AND field <> ?2")
        .map_err(st)?;
    let clocks: Vec<String> = stmt
        .query_map(params![id, DEL_CLOCK], |r| r.get::<_, String>(0))
        .map_err(st)?
        .collect::<rusqlite::Result<_>>()
        .map_err(st)?;
    drop(stmt);
    let mut max = zero_hlc();
    for c in clocks {
        if let Ok(h) = Hlc::decode(&c) {
            if h > max {
                max = h;
            }
        }
    }
    Ok(max)
}

/// Recompute `contexts.deleted` from the stored clocks — whole-document LWW.
///
/// The tombstone stands while its clock is at least the highest write clock;
/// a strictly later write resurrects the document. `>=` rather than `>` because
/// a delete and the writes it accompanies are stamped with the SAME clock (see
/// `write_context` and `ops_for_context`), and the delete must win that tie —
/// it is one edit, not two racing ones.
fn refresh_context_deleted(conn: &Connection, id: &str) -> Result<bool> {
    let del_s: Option<String> = conn
        .query_row("SELECT del_hlc FROM contexts WHERE id = ?1", [id], |r| {
            r.get(0)
        })
        .optional()
        .map_err(st)?
        .flatten();
    let del = dec_hlc(del_s)?;
    let writes = context_write_clock(conn, id)?;
    let deleted = matches!(&del, Some(d) if d >= &writes);
    conn.execute(
        "UPDATE contexts SET deleted = ?2 WHERE id = ?1",
        params![id, deleted as i64],
    )
    .map_err(st)?;
    Ok(deleted)
}

/// The category mirror of [`refresh_context_deleted`], reading the tombstone
/// clock out of the clock map under [`DEL_CLOCK`].
fn refresh_category_deleted(conn: &Connection, id: &str) -> Result<bool> {
    let del = category_field_clock(conn, id, DEL_CLOCK)?;
    let writes = category_write_clock(conn, id)?;
    let deleted = matches!(&del, Some(d) if d >= &writes);
    conn.execute(
        "UPDATE categories SET deleted = ?2 WHERE id = ?1",
        params![id, deleted as i64],
    )
    .map_err(st)?;
    Ok(deleted)
}

/// Raise `categories.hlc` to `hlc` if that is higher. The column is the
/// document's high-water clock, which is what `map_category` reports as
/// `Category::hlc`.
fn bump_category_hlc(conn: &Connection, id: &str, hlc: &Hlc) -> Result<()> {
    let cur_s: Option<String> = conn
        .query_row("SELECT hlc FROM categories WHERE id = ?1", [id], |r| {
            r.get(0)
        })
        .optional()
        .map_err(st)?;
    let higher = match dec_hlc(cur_s)? {
        Some(cur) => hlc > &cur,
        None => true,
    };
    if higher {
        conn.execute(
            "UPDATE categories SET hlc = ?2 WHERE id = ?1",
            params![id, hlc.encode()],
        )
        .map_err(st)?;
    }
    Ok(())
}

/// Resolve the local row a category op addresses, creating a placeholder if we
/// have never seen it.
///
/// `idx_cat_key` is UNIQUE on `key`, so `key` — not the id — is a category's
/// effective identity here: two peers that independently minted a category for
/// `feature.api` must converge on ONE row, and inserting the second id would
/// just fail the constraint. Returns the row id whose clocks the caller must
/// use.
fn ensure_category_row(conn: &Connection, id: &CategoryId, key: &str, hlc: &Hlc) -> Result<String> {
    let by_id: Option<String> = conn
        .query_row("SELECT id FROM categories WHERE id = ?1", [&id.0], |r| {
            r.get(0)
        })
        .optional()
        .map_err(st)?;
    if let Some(existing) = by_id {
        return Ok(existing);
    }
    let by_key: Option<String> = conn
        .query_row("SELECT id FROM categories WHERE key = ?1", [key], |r| {
            r.get(0)
        })
        .optional()
        .map_err(st)?;
    if let Some(existing) = by_key {
        return Ok(existing);
    }
    conn.execute(
        "INSERT INTO categories (id, key, label, parent_key, color, source, taxonomy_version, hlc, deleted)
         VALUES (?1, ?2, '', NULL, NULL, ?3, NULL, ?4, 0)",
        params![id.0, key, CategorySource::Peer.as_str(), hlc.encode()],
    )
    .map_err(st)?;
    Ok(id.0.clone())
}

/// Merge one OR-Set member op. Each side of the element carries its own clock
/// and only ever moves forward, so the result is independent of arrival order;
/// `reconstruct_context` then reads the element as present when
/// `add_hlc >= remove_hlc` (add wins the tie).
#[allow(clippy::too_many_arguments)]
fn merge_context_member(
    conn: &Connection,
    id: &str,
    kind: &str,
    key: &str,
    note: Option<&str>,
    add: bool,
    hlc: &Hlc,
) -> Result<bool> {
    let existing: Option<(Option<String>, Option<String>)> = conn
        .query_row(
            "SELECT add_hlc, remove_hlc FROM context_members
             WHERE context_id = ?1 AND member_kind = ?2 AND member_key = ?3",
            params![id, kind, key],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .optional()
        .map_err(st)?;
    let (add_h, rem_h) = match existing {
        Some((a, r)) => (dec_hlc(a)?, dec_hlc(r)?),
        None => (None, None),
    };
    // The side this op writes only moves forward — an older or repeated op loses.
    let current = if add { &add_h } else { &rem_h };
    if matches!(current, Some(c) if c >= hlc) {
        return Ok(false);
    }
    // Two statements, not one with COALESCE: on an ADD the element's `note` is
    // an LWW scalar carried by the winning add, so it must be written even when
    // the op clears it to NULL. A REMOVE carries no note and must leave the
    // stored one alone — a later resurrection keeps the note it had.
    if add {
        conn.execute(
            "INSERT INTO context_members (context_id, member_kind, member_key, note, add_hlc, remove_hlc)
             VALUES (?1, ?2, ?3, ?4, ?5, NULL)
             ON CONFLICT(context_id, member_kind, member_key) DO UPDATE SET
                note    = excluded.note,
                add_hlc = excluded.add_hlc",
            params![id, kind, key, note, hlc.encode()],
        )
        .map_err(st)?;
    } else {
        conn.execute(
            "INSERT INTO context_members (context_id, member_kind, member_key, note, add_hlc, remove_hlc)
             VALUES (?1, ?2, ?3, NULL, NULL, ?4)
             ON CONFLICT(context_id, member_kind, member_key) DO UPDATE SET
                remove_hlc = excluded.remove_hlc",
            params![id, kind, key, hlc.encode()],
        )
        .map_err(st)?;
    }
    Ok(true)
}

/// Replay a single remote op into the typed rows. Returns whether it changed
/// anything.
fn merge_op(conn: &Connection, op: &SyncOp) -> Result<bool> {
    match op {
        SyncOp::ContextLww {
            id,
            field,
            value,
            hlc,
        } => {
            ensure_context_row(conn, id, "")?;
            let name = field.as_str();
            if let Some(cur) = context_field_clock(conn, &id.0, name)? {
                if &cur >= hlc {
                    return Ok(false);
                }
            }
            // One statement per field: the column name is not a bindable
            // parameter and must never be interpolated from data.
            let sql = match field {
                CtxField::Name => "UPDATE contexts SET name = ?2, updated_at = ?3 WHERE id = ?1",
                CtxField::Description => {
                    "UPDATE contexts SET description = ?2, updated_at = ?3 WHERE id = ?1"
                }
                CtxField::Notes => "UPDATE contexts SET notes = ?2, updated_at = ?3 WHERE id = ?1",
            };
            conn.execute(sql, params![id.0, value, now_rfc3339()])
                .map_err(st)?;
            set_context_field_clock(conn, id, name, hlc)?;
            refresh_context_deleted(conn, &id.0)?;
            Ok(true)
        }
        SyncOp::ContextTag { id, tag, add, hlc } => {
            ensure_context_row(conn, id, "")?;
            let changed = merge_context_member(conn, &id.0, "tag", tag, None, *add, hlc)?;
            if changed {
                refresh_context_deleted(conn, &id.0)?;
            }
            Ok(changed)
        }
        SyncOp::ContextRepo {
            id,
            repo_id,
            add,
            hlc,
        } => {
            ensure_context_row(conn, id, "")?;
            let changed = merge_context_member(conn, &id.0, "repo", &repo_id.0, None, *add, hlc)?;
            if changed {
                refresh_context_deleted(conn, &id.0)?;
            }
            Ok(changed)
        }
        SyncOp::ContextPr {
            id,
            repo_slug,
            number,
            note,
            add,
            hlc,
        } => {
            ensure_context_row(conn, id, "")?;
            // Same "slug#number" member key `write_context` writes and
            // `reconstruct_context` parses back.
            let key = format!("{repo_slug}#{number}");
            let changed =
                merge_context_member(conn, &id.0, "pr", &key, note.as_deref(), *add, hlc)?;
            if changed {
                refresh_context_deleted(conn, &id.0)?;
            }
            Ok(changed)
        }
        SyncOp::ContextDel { id, hlc } => {
            ensure_context_row(conn, id, "")?;
            let cur_s: Option<String> = conn
                .query_row("SELECT del_hlc FROM contexts WHERE id = ?1", [&id.0], |r| {
                    r.get(0)
                })
                .optional()
                .map_err(st)?
                .flatten();
            if matches!(dec_hlc(cur_s)?, Some(c) if &c >= hlc) {
                return Ok(false);
            }
            conn.execute(
                "UPDATE contexts SET del_hlc = ?2 WHERE id = ?1",
                params![id.0, hlc.encode()],
            )
            .map_err(st)?;
            // Not unconditionally deleted: a write with a HIGHER clock that
            // already landed outranks this tombstone.
            refresh_context_deleted(conn, &id.0)?;
            Ok(true)
        }
        SyncOp::CategoryLww {
            id,
            key,
            field,
            value,
            hlc,
        } => {
            let row = ensure_category_row(conn, id, key, hlc)?;
            let name = field.as_str();
            if let Some(cur) = category_field_clock(conn, &row, name)? {
                if &cur >= hlc {
                    return Ok(false);
                }
            }
            // `color` and `parent_key` are nullable; the op envelope carries
            // "unset" as the empty string (see `ops_for_category`).
            let sql = match field {
                CatField::Label => "UPDATE categories SET label = ?2 WHERE id = ?1",
                CatField::Color => "UPDATE categories SET color = NULLIF(?2, '') WHERE id = ?1",
                CatField::ParentKey => {
                    "UPDATE categories SET parent_key = NULLIF(?2, '') WHERE id = ?1"
                }
            };
            conn.execute(sql, params![row, value]).map_err(st)?;
            set_category_field_clock(conn, &row, name, hlc)?;
            bump_category_hlc(conn, &row, hlc)?;
            refresh_category_deleted(conn, &row)?;
            Ok(true)
        }
        SyncOp::CategoryDel { id, hlc } => {
            // A delete for a category we have never seen still has to be
            // recorded, or a later resurrection would have nothing to outrank.
            // `key` is unknown here, so the placeholder is keyed on the id.
            let row = ensure_category_row(conn, id, &id.0, hlc)?;
            if let Some(cur) = category_field_clock(conn, &row, DEL_CLOCK)? {
                if &cur >= hlc {
                    return Ok(false);
                }
            }
            set_category_field_clock(conn, &row, DEL_CLOCK, hlc)?;
            bump_category_hlc(conn, &row, hlc)?;
            refresh_category_deleted(conn, &row)?;
            Ok(true)
        }
    }
}

/// Is this exact op already in the log? Keeps re-delivery from growing the log
/// without bound. Narrowed by `idx_sync_hlc` first, so it is an index probe and
/// not a table scan.
fn op_already_logged(conn: &Connection, op: &SyncOp) -> Result<bool> {
    let json = serde_json::to_string(op)?;
    let hit: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM sync_ops WHERE hlc = ?1 AND op_json = ?2 LIMIT 1",
            params![op.hlc().encode(), json],
            |r| r.get(0),
        )
        .optional()
        .map_err(st)?;
    Ok(hit.is_some())
}

fn append_ops(conn: &Connection, ops: &[SyncOp]) -> Result<()> {
    for op in ops {
        let json = serde_json::to_string(op)?;
        conn.execute(
            "INSERT INTO sync_ops (op_json, hlc, applied) VALUES (?1, ?2, 1)",
            params![json, op.hlc().encode()],
        )
        .map_err(st)?;
    }
    observe_ops(conn, ops)
}

/// Fold every appended op's clock into the stored `hlc_last` — the HLC receive
/// rule (see [`Hlc::observe`]). Every op reaching the log passes through here,
/// so a remote op can no longer leave this node minting clocks *below* an op it
/// has already seen; for a local op the fold is a no-op, since [`next_hlc`]
/// already wrote that same reading.
///
/// A remote wall clock more than [`HLC_SKEW_MS`] ahead of ours is recorded but
/// not folded: it is skew (or hostility), not time that has passed, and
/// following it would strand this node's clock in the future for good. Ops are
/// never dropped here — refusing to fold costs us the causal edge with that one
/// peer; dropping would cost the data.
fn observe_ops(conn: &Connection, ops: &[SyncOp]) -> Result<()> {
    let ceiling = gitstate_core::now_wall_ms().saturating_add(HLC_SKEW_MS);
    let Some(highest) = ops
        .iter()
        .map(|o| o.hlc())
        .filter(|h| h.wall_ms <= ceiling)
        .max()
    else {
        return Ok(());
    };
    // `hlc_last` is this node's own reading, so a fresh one is spelled with the
    // local peer id — `observe` never adopts the remote's identity.
    let mut last = match dec_hlc(kv_get_conn(conn, "hlc_last")?)? {
        Some(h) => h,
        None => Hlc {
            wall_ms: 0,
            counter: 0,
            peer: get_or_create_peer(conn)?,
        },
    };
    let before = last.clone();
    last.observe(highest);
    if last.wall_ms != before.wall_ms || last.counter != before.counter {
        kv_set_conn(conn, "hlc_last", &last.encode())?;
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

    fn list_commits(&self, repo: Option<&RepoId>) -> Result<Vec<Commit>> {
        let conn = self.conn.lock().unwrap();
        // Oldest first so every downstream series is already in chronological
        // order; the secondary sha sort keeps same-second commits stable.
        let rows = match repo {
            Some(r) => {
                let mut stmt = conn
                    .prepare(&format!(
                        "SELECT {COMMIT_COLS} FROM commits WHERE repo_id = ?1
                         ORDER BY committed_at, sha"
                    ))
                    .map_err(st)?;
                let v = stmt
                    .query_map([&r.0], map_commit)
                    .map_err(st)?
                    .collect::<rusqlite::Result<Vec<_>>>()
                    .map_err(st)?;
                v
            }
            None => {
                let mut stmt = conn
                    .prepare(&format!(
                        "SELECT {COMMIT_COLS} FROM commits ORDER BY committed_at, sha"
                    ))
                    .map_err(st)?;
                let v = stmt
                    .query_map([], map_commit)
                    .map_err(st)?
                    .collect::<rusqlite::Result<Vec<_>>>()
                    .map_err(st)?;
                v
            }
        };
        Ok(rows)
    }

    fn list_all_work_items(&self) -> Result<Vec<WorkItem>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {WI_COLS} FROM work_items ORDER BY created_at DESC, id"
            ))
            .map_err(st)?;
        let rows = stmt
            .query_map([], map_work_item)
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

    fn list_classifications(&self, repo: &RepoId) -> Result<Vec<Classification>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(
                "SELECT c.item_id, c.category_key, c.confidence, c.method, c.rationale
                 FROM classifications c
                 JOIN work_items w ON w.id = c.item_id
                 WHERE w.repo_id = ?1",
            )
            .map_err(st)?;
        let rows = stmt
            .query_map([&repo.0], |r| {
                Ok(Classification {
                    item_id: WorkItemId(r.get(0)?),
                    category_key: r.get(1)?,
                    confidence: r.get(2)?,
                    method: EffortMethod::parse(&r.get::<_, String>(3)?)
                        .unwrap_or(EffortMethod::Heuristic),
                    rationale: r.get(4)?,
                })
            })
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
    }

    fn list_effort(&self, repo: &RepoId) -> Result<Vec<EffortEstimate>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn
            .prepare(
                "SELECT e.item_id, e.difficulty, e.method, e.rationale, e.confidence
                 FROM effort e
                 JOIN work_items w ON w.id = e.item_id
                 WHERE w.repo_id = ?1",
            )
            .map_err(st)?;
        let rows = stmt
            .query_map([&repo.0], |r| {
                Ok(EffortEstimate {
                    item_id: WorkItemId(r.get(0)?),
                    difficulty: r.get(1)?,
                    method: EffortMethod::parse(&r.get::<_, String>(2)?)
                        .unwrap_or(EffortMethod::Heuristic),
                    rationale: r.get(3)?,
                    confidence: r.get(4)?,
                })
            })
            .map_err(st)?
            .collect::<rusqlite::Result<_>>()
            .map_err(st)?;
        Ok(rows)
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

    fn merge_sync_op(&self, op: &SyncOp) -> Result<bool> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction().map_err(st)?;
        let changed = merge_op(&tx, op)?;
        // Row effect and log entry commit together: a peer's op can never be
        // recorded as "seen" without having moved the rows, nor vice versa.
        if !op_already_logged(&tx, op)? {
            append_ops(&tx, std::slice::from_ref(op))?;
        }
        tx.commit().map_err(st)?;
        Ok(changed)
    }

    /// Returned in `seq` order — the order ops reached THIS node, which is not
    /// the HLC order. That is deliberate and sufficient: `merge_sync_op` is
    /// commutative and idempotent, so a peer converges on the same rows
    /// whatever order it receives them in. Sorting by clock here would buy
    /// nothing and would break the `since` cursor, which is a watermark over
    /// arrival, not over wall time.
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

    /// The HLC receive rule: ingesting a remote op folds its clock into the
    /// local one, so the very next local edit sorts *after* the op it causally
    /// follows even when this node's wall clock trails the peer's. Without the
    /// fold the local edit mints a lower clock and LWW keeps the remote's older
    /// write forever.
    #[test]
    fn local_clock_sorts_after_an_ingested_remote_op() {
        let s = store();
        // A peer whose wall clock runs ahead of ours (inside the skew bound).
        let remote = Hlc {
            wall_ms: gitstate_core::now_wall_ms() + 30_000,
            counter: 7,
            peer: PeerId::from("remote-peer"),
        };
        s.append_sync_ops(&[SyncOp::ContextLww {
            id: ContextId::from("c1"),
            field: CtxField::Name,
            value: "from the peer".into(),
            hlc: remote.clone(),
        }])
        .unwrap();

        let conn = s.conn.lock().unwrap();
        let local = next_hlc(&conn).unwrap();
        drop(conn);
        assert!(
            local > remote,
            "local clock {local:?} must sort after the observed remote {remote:?}"
        );
    }

    /// The fold is bounded: a peer claiming a wall clock far in the future
    /// cannot drag this node's clock along with it. The op is still recorded.
    #[test]
    fn a_wildly_skewed_remote_clock_is_not_folded_in() {
        let s = store();
        let absurd = Hlc {
            wall_ms: gitstate_core::now_wall_ms() + 10 * HLC_SKEW_MS,
            counter: 0,
            peer: PeerId::from("skewed-peer"),
        };
        s.append_sync_ops(&[SyncOp::ContextDel {
            id: ContextId::from("c1"),
            hlc: absurd.clone(),
        }])
        .unwrap();
        assert_eq!(
            s.sync_ops_since(None).unwrap().len(),
            1,
            "op still recorded"
        );

        let conn = s.conn.lock().unwrap();
        let local = next_hlc(&conn).unwrap();
        drop(conn);
        assert!(
            local < absurd,
            "local clock {local:?} must not have followed the skewed peer"
        );
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

    // ───────────────── remote op replay (merge_sync_op) ─────────────────

    fn hlc_at(wall_ms: u64, peer: &str) -> Hlc {
        Hlc {
            wall_ms,
            counter: 0,
            peer: PeerId::from(peer),
        }
    }

    fn ctx_name(id: &str, value: &str, hlc: Hlc) -> SyncOp {
        SyncOp::ContextLww {
            id: ContextId::from(id),
            field: CtxField::Name,
            value: value.into(),
            hlc,
        }
    }

    fn ctx_tag(id: &str, tag: &str, add: bool, hlc: Hlc) -> SyncOp {
        SyncOp::ContextTag {
            id: ContextId::from(id),
            tag: tag.into(),
            add,
            hlc,
        }
    }

    /// A comparable fingerprint of everything a peer is supposed to converge on.
    fn snapshot(s: &SqliteStore) -> Vec<String> {
        let mut out = Vec::new();
        for id in ["c1", "c2"] {
            if let Some(c) = s.get_context(&ContextId::from(id)).unwrap() {
                let mut tags = c.tags.clone();
                tags.sort();
                let mut repos: Vec<String> = c.repo_ids.iter().map(|r| r.0.clone()).collect();
                repos.sort();
                out.push(format!(
                    "{id}|{}|{}|{}|{:?}|{:?}",
                    c.name, c.description, c.deleted, tags, repos
                ));
            }
        }
        for c in s.list_categories().unwrap() {
            out.push(format!("cat:{}|{}|{:?}", c.key, c.label, c.color));
        }
        out
    }

    fn permutations<T: Clone>(items: &[T]) -> Vec<Vec<T>> {
        if items.len() <= 1 {
            return vec![items.to_vec()];
        }
        let mut out = Vec::new();
        for i in 0..items.len() {
            let mut rest = items.to_vec();
            let head = rest.remove(i);
            for mut tail in permutations(&rest) {
                tail.insert(0, head.clone());
                out.push(tail);
            }
        }
        out
    }

    /// THE regression. `apply_op` used to only append remote ops to `sync_ops`,
    /// so a merged op left contexts and categories exactly as they were: two
    /// peers exchanged history and neither one's screen changed. Appending is
    /// history; merging is state, and they are now different calls.
    #[test]
    fn appending_a_remote_op_does_not_move_rows_but_merging_does() {
        let s = store();
        let op = ctx_name("c1", "from the peer", hlc_at(10, "peer-a"));

        s.append_sync_ops(std::slice::from_ref(&op)).unwrap();
        assert!(
            s.get_context(&ContextId::from("c1")).unwrap().is_none(),
            "append_sync_ops records history only — it must not fabricate rows"
        );

        assert!(s.merge_sync_op(&op).unwrap(), "merge reports a change");
        let got = s.get_context(&ContextId::from("c1")).unwrap().unwrap();
        assert_eq!(got.name, "from the peer", "merge replays into the row");
    }

    /// Per-field LWW: the higher clock wins regardless of arrival order, and
    /// only the field it names moves.
    #[test]
    fn scalar_fields_are_last_writer_wins_by_clock() {
        for order in [[0usize, 1], [1, 0]] {
            let s = store();
            let ops = [
                ctx_name("c1", "older", hlc_at(10, "peer-a")),
                ctx_name("c1", "newer", hlc_at(20, "peer-b")),
            ];
            for i in order {
                s.merge_sync_op(&ops[i]).unwrap();
            }
            assert_eq!(
                s.get_context(&ContextId::from("c1")).unwrap().unwrap().name,
                "newer",
                "arrival order {order:?} must not decide the winner"
            );
        }
    }

    /// An op older than what the row already holds is recorded but loses.
    #[test]
    fn an_older_remote_write_loses_and_reports_no_change() {
        let s = store();
        s.merge_sync_op(&ctx_name("c1", "newer", hlc_at(20, "peer-b")))
            .unwrap();
        assert!(
            !s.merge_sync_op(&ctx_name("c1", "older", hlc_at(10, "peer-a")))
                .unwrap(),
            "a losing op reports no state change"
        );
        assert_eq!(
            s.get_context(&ContextId::from("c1")).unwrap().unwrap().name,
            "newer"
        );
    }

    /// OR-Set: add wins a tie against a remove at the same clock, and each side
    /// only moves forward.
    #[test]
    fn set_members_are_an_add_wins_or_set() {
        // remove at a LOWER clock than the add — the tag stays.
        let s = store();
        s.merge_sync_op(&ctx_tag("c1", "keep", false, hlc_at(5, "p")))
            .unwrap();
        s.merge_sync_op(&ctx_tag("c1", "keep", true, hlc_at(9, "p")))
            .unwrap();
        assert_eq!(
            s.get_context(&ContextId::from("c1")).unwrap().unwrap().tags,
            vec!["keep".to_string()]
        );

        // remove at a HIGHER clock — the tag goes.
        s.merge_sync_op(&ctx_tag("c1", "keep", false, hlc_at(12, "p")))
            .unwrap();
        assert!(s
            .get_context(&ContextId::from("c1"))
            .unwrap()
            .unwrap()
            .tags
            .is_empty());

        // add-wins on an exact tie.
        let s2 = store();
        s2.merge_sync_op(&ctx_tag("c1", "tie", false, hlc_at(7, "p")))
            .unwrap();
        s2.merge_sync_op(&ctx_tag("c1", "tie", true, hlc_at(7, "p")))
            .unwrap();
        assert_eq!(
            s2.get_context(&ContextId::from("c1"))
                .unwrap()
                .unwrap()
                .tags,
            vec!["tie".to_string()],
            "add wins the tie"
        );
    }

    /// Whole-doc tombstone: a strictly later write resurrects, an earlier one
    /// does not — in either arrival order.
    #[test]
    fn a_later_write_resurrects_a_tombstoned_context() {
        for order in [[0usize, 1], [1, 0]] {
            let s = store();
            let ops = [
                SyncOp::ContextDel {
                    id: ContextId::from("c1"),
                    hlc: hlc_at(20, "peer-a"),
                },
                ctx_name("c1", "back from the dead", hlc_at(30, "peer-b")),
            ];
            for i in order {
                s.merge_sync_op(&ops[i]).unwrap();
            }
            let got = s.get_context(&ContextId::from("c1")).unwrap().unwrap();
            assert!(!got.deleted, "later write resurrects (order {order:?})");
            assert_eq!(got.name, "back from the dead");
            assert_eq!(s.list_contexts().unwrap().len(), 1);
        }
    }

    #[test]
    fn an_earlier_write_does_not_resurrect_a_tombstoned_context() {
        for order in [[0usize, 1], [1, 0]] {
            let s = store();
            let ops = [
                SyncOp::ContextDel {
                    id: ContextId::from("c1"),
                    hlc: hlc_at(30, "peer-a"),
                },
                ctx_name("c1", "stale edit", hlc_at(10, "peer-b")),
            ];
            for i in order {
                s.merge_sync_op(&ops[i]).unwrap();
            }
            let got = s.get_context(&ContextId::from("c1")).unwrap().unwrap();
            assert!(got.deleted, "tombstone stands (order {order:?})");
            assert!(s.list_contexts().unwrap().is_empty());
        }
    }

    /// The property the whole design rests on: every arrival order of the same
    /// op set lands on byte-identical state, and replaying the set twice
    /// changes nothing.
    #[test]
    fn merge_is_commutative_and_idempotent_over_every_arrival_order() {
        let ops = vec![
            ctx_name("c1", "first", hlc_at(10, "peer-a")),
            ctx_name("c1", "second", hlc_at(40, "peer-b")),
            ctx_tag("c1", "alpha", true, hlc_at(20, "peer-a")),
            SyncOp::ContextRepo {
                id: ContextId::from("c1"),
                repo_id: RepoId::from("r1"),
                add: true,
                hlc: hlc_at(25, "peer-b"),
            },
            SyncOp::CategoryLww {
                id: CategoryId::from("cat-1"),
                key: "feature.api".into(),
                field: CatField::Label,
                value: "API".into(),
                hlc: hlc_at(30, "peer-a"),
            },
            SyncOp::ContextDel {
                id: ContextId::from("c1"),
                hlc: hlc_at(15, "peer-a"),
            },
        ];

        let reference = {
            let s = store();
            for op in &ops {
                s.merge_sync_op(op).unwrap();
            }
            snapshot(&s)
        };
        assert!(
            !reference.is_empty(),
            "the fixture must actually produce state, or this test proves nothing"
        );

        let orders = permutations(&ops);
        assert_eq!(orders.len(), 720, "6! arrival orders");
        for order in &orders {
            let s = store();
            for op in order {
                s.merge_sync_op(op).unwrap();
            }
            assert_eq!(snapshot(&s), reference, "diverged on one arrival order");

            // ... and again, in reverse, on top of itself.
            for op in order.iter().rev() {
                assert!(
                    !s.merge_sync_op(op).unwrap(),
                    "a replayed op must report no change"
                );
            }
            assert_eq!(snapshot(&s), reference, "replay was not idempotent");
        }
    }

    /// Re-delivering an op does not grow the log.
    #[test]
    fn redelivering_an_op_does_not_duplicate_the_log() {
        let s = store();
        let op = ctx_name("c1", "once", hlc_at(10, "peer-a"));
        s.merge_sync_op(&op).unwrap();
        s.merge_sync_op(&op).unwrap();
        s.merge_sync_op(&op).unwrap();
        assert_eq!(s.sync_ops_since(None).unwrap().len(), 1);
    }

    /// A merged op is re-exported, so this node relays what it learned.
    #[test]
    fn a_merged_op_is_re_exported_to_the_next_peer() {
        let s = store();
        let op = ctx_name("c1", "relayed", hlc_at(10, "peer-a"));
        s.merge_sync_op(&op).unwrap();
        let exported = s.sync_ops_since(None).unwrap();
        assert_eq!(exported.len(), 1);
        assert_eq!(
            serde_json::to_string(&exported[0]).unwrap(),
            serde_json::to_string(&op).unwrap()
        );
    }

    /// Categories converge too, including delete + resurrect, and two peers
    /// that minted different ids for the same key land on one row.
    #[test]
    fn category_ops_merge_delete_and_resurrect() {
        let s = store();
        let cat = |id: &str, field, value: &str, wall| SyncOp::CategoryLww {
            id: CategoryId::from(id),
            key: "feature.api".into(),
            field,
            value: value.into(),
            hlc: hlc_at(wall, "peer-a"),
        };

        s.merge_sync_op(&cat("cat-1", CatField::Label, "API", 10))
            .unwrap();
        s.merge_sync_op(&cat("cat-1", CatField::Color, "#4f46e5", 10))
            .unwrap();
        let got = s.get_category("feature.api").unwrap().unwrap();
        assert_eq!(got.label, "API");
        assert_eq!(got.color.as_deref(), Some("#4f46e5"));
        assert_eq!(got.source, CategorySource::Peer);

        // A different peer's id for the SAME key merges into the one row —
        // `idx_cat_key` is unique, so key is the effective identity.
        s.merge_sync_op(&cat("cat-2", CatField::Label, "API v2", 20))
            .unwrap();
        assert_eq!(s.list_categories().unwrap().len(), 1);
        assert_eq!(
            s.get_category("feature.api").unwrap().unwrap().label,
            "API v2"
        );

        // Tombstone, then resurrect with a later write.
        s.merge_sync_op(&SyncOp::CategoryDel {
            id: CategoryId::from("cat-1"),
            hlc: hlc_at(30, "peer-a"),
        })
        .unwrap();
        assert!(s.list_categories().unwrap().is_empty());
        s.merge_sync_op(&cat("cat-1", CatField::Label, "API v3", 40))
            .unwrap();
        assert_eq!(s.list_categories().unwrap().len(), 1);
        assert_eq!(
            s.get_category("feature.api").unwrap().unwrap().label,
            "API v3"
        );
    }

    /// A locally deleted category must survive a later merge of an OLDER
    /// remote write — the local delete has to leave a tombstone clock behind,
    /// not just a `deleted` flag.
    #[test]
    fn a_local_category_delete_is_not_undone_by_an_older_remote_write() {
        let s = store();
        let cat = Category {
            id: CategoryId::from("cat-1"),
            key: "feature.api".into(),
            label: "API".into(),
            parent_key: None,
            color: None,
            source: CategorySource::Local,
            taxonomy_version: None,
            hlc: zero_hlc(),
            deleted: false,
        };
        s.upsert_category(&cat).unwrap();
        let mut gone = cat.clone();
        gone.deleted = true;
        s.upsert_category(&gone).unwrap();
        assert!(s.list_categories().unwrap().is_empty());

        // An ancient remote label edit arrives afterwards.
        s.merge_sync_op(&SyncOp::CategoryLww {
            id: CategoryId::from("cat-1"),
            key: "feature.api".into(),
            field: CatField::Label,
            value: "resurrected?".into(),
            hlc: hlc_at(1, "peer-a"),
        })
        .unwrap();
        assert!(
            s.list_categories().unwrap().is_empty(),
            "an older remote write must not undo a local delete"
        );
    }

    /// Same guarantee on the context side, where the tombstone clock lives in
    /// `contexts.del_hlc`.
    #[test]
    fn a_local_context_delete_is_not_undone_by_an_older_remote_write() {
        let s = store();
        let ctx = Context {
            id: ContextId::from("c1"),
            name: "local".into(),
            description: String::new(),
            repo_ids: vec![],
            pr_refs: vec![],
            notes: String::new(),
            tags: vec![],
            created_at: now_rfc3339(),
            updated_at: now_rfc3339(),
            hlc: zero_hlc(),
            deleted: false,
        };
        s.upsert_context(&ctx).unwrap();
        let mut gone = ctx.clone();
        gone.deleted = true;
        s.upsert_context(&gone).unwrap();
        assert!(s.list_contexts().unwrap().is_empty());

        s.merge_sync_op(&ctx_name("c1", "resurrected?", hlc_at(1, "peer-a")))
            .unwrap();
        assert!(
            s.list_contexts().unwrap().is_empty(),
            "an older remote write must not undo a local delete"
        );
    }

    /// PR refs carry a note and round-trip through the "slug#number" member key.
    #[test]
    fn pr_ref_ops_merge_into_the_context() {
        let s = store();
        s.merge_sync_op(&SyncOp::ContextPr {
            id: ContextId::from("c1"),
            repo_slug: "vul-os/gitstate".into(),
            number: 42,
            note: Some("the fix".into()),
            add: true,
            hlc: hlc_at(10, "peer-a"),
        })
        .unwrap();
        let got = s.get_context(&ContextId::from("c1")).unwrap().unwrap();
        assert_eq!(got.pr_refs.len(), 1);
        assert_eq!(got.pr_refs[0].repo_slug, "vul-os/gitstate");
        assert_eq!(got.pr_refs[0].number, 42);
        assert_eq!(got.pr_refs[0].note.as_deref(), Some("the fix"));
    }

    /// End to end: peer B's log, replayed into peer A, reproduces peer B's
    /// object. This is what "sync works" means, and what the old append-only
    /// `apply_op` never did.
    #[test]
    fn one_peers_log_replayed_into_another_reproduces_the_object() {
        let a = store();
        let b = store();

        let ctx = Context {
            id: ContextId::from("c1"),
            name: "Q3 refactor".into(),
            description: "cleanup".into(),
            repo_ids: vec![RepoId::from("r1")],
            pr_refs: vec![ContextPrRef {
                repo_slug: "vul-os/gitstate".into(),
                number: 7,
                note: None,
            }],
            notes: "notes".into(),
            tags: vec!["refactor".into(), "q3".into()],
            created_at: now_rfc3339(),
            updated_at: now_rfc3339(),
            hlc: zero_hlc(),
            deleted: false,
        };
        b.upsert_context(&ctx).unwrap();
        let from_b = b.get_context(&ctx.id).unwrap().unwrap();

        for op in b.sync_ops_since(None).unwrap() {
            a.merge_sync_op(&op).unwrap();
        }
        let on_a = a.get_context(&ctx.id).unwrap().unwrap();

        assert_eq!(on_a.name, from_b.name);
        assert_eq!(on_a.description, from_b.description);
        assert_eq!(on_a.notes, from_b.notes);
        let (mut x, mut y) = (on_a.tags.clone(), from_b.tags.clone());
        x.sort();
        y.sort();
        assert_eq!(x, y);
        assert_eq!(on_a.repo_ids, from_b.repo_ids);
        assert_eq!(on_a.pr_refs.len(), 1);
        assert_eq!(on_a.pr_refs[0].number, 7);
        assert!(!on_a.deleted);
    }

    /// Register a repo so the FK on `commits`/`work_items` is satisfiable.
    fn seed_repo(s: &SqliteStore, id: &str) -> RepoId {
        let rid = RepoId(id.into());
        s.upsert_repo(&Repo {
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

    fn commit_at(repo: &RepoId, sha: &str, at: &str) -> Commit {
        Commit {
            sha: sha.into(),
            repo_id: repo.clone(),
            author_email: "dev@example.com".into(),
            author_name: "Dev".into(),
            committed_at: at.into(),
            additions: 10,
            deletions: 2,
            files_changed: 1,
            is_merge: false,
            is_test_touch: true,
            summary: "work".into(),
        }
    }

    #[test]
    fn list_commits_returns_oldest_first_and_preserves_flags() {
        let s = store();
        let r = seed_repo(&s, "r1");
        s.save_commits(
            &r,
            &[
                commit_at(&r, "ccc", "2026-06-03T00:00:00Z"),
                commit_at(&r, "aaa", "2026-06-01T00:00:00Z"),
                commit_at(&r, "bbb", "2026-06-02T00:00:00Z"),
            ],
        )
        .unwrap();

        let all = s.list_commits(None).unwrap();
        let shas: Vec<&str> = all.iter().map(|c| c.sha.as_str()).collect();
        assert_eq!(shas, vec!["aaa", "bbb", "ccc"]);
        assert!(all[0].is_test_touch, "bool columns survive the round-trip");
        assert!(!all[0].is_merge);
        assert_eq!(all[0].additions, 10);
        assert_eq!(all[0].repo_id, r);
    }

    #[test]
    fn list_commits_scopes_to_one_repo() {
        let s = store();
        let r1 = seed_repo(&s, "r1");
        let r2 = seed_repo(&s, "r2");
        s.save_commits(&r1, &[commit_at(&r1, "aaa", "2026-06-01T00:00:00Z")])
            .unwrap();
        s.save_commits(&r2, &[commit_at(&r2, "bbb", "2026-06-02T00:00:00Z")])
            .unwrap();

        assert_eq!(s.list_commits(None).unwrap().len(), 2);
        let scoped = s.list_commits(Some(&r1)).unwrap();
        assert_eq!(scoped.len(), 1);
        assert_eq!(scoped[0].sha, "aaa");
        // An unknown repo is empty, not an error.
        assert!(s
            .list_commits(Some(&RepoId("nope".into())))
            .unwrap()
            .is_empty());
    }

    #[test]
    fn list_all_work_items_spans_every_repo() {
        let s = store();
        let r1 = seed_repo(&s, "r1");
        let r2 = seed_repo(&s, "r2");
        let item = |repo: &RepoId, id: &str, created: &str| WorkItem {
            id: WorkItemId(id.into()),
            repo_id: repo.clone(),
            kind: WorkKind::Pr,
            external_ref: format!("#{id}"),
            title: "t".into(),
            body: String::new(),
            state: WorkState::Merged,
            author_login: None,
            labels: vec!["backend".into()],
            created_at: created.into(),
            updated_at: created.into(),
            merged_at: Some(created.into()),
            closed_at: None,
            files_touched: vec!["a.rs".into()],
        };
        s.save_work_items(&[
            item(&r1, "1", "2026-06-01T00:00:00Z"),
            item(&r2, "2", "2026-06-05T00:00:00Z"),
        ])
        .unwrap();

        let all = s.list_all_work_items().unwrap();
        assert_eq!(all.len(), 2);
        assert_eq!(all[0].id.0, "2", "newest first");
        assert_eq!(all[0].labels, vec!["backend".to_string()]);
        assert_eq!(s.list_work_items(&r1).unwrap().len(), 1);
    }

    #[test]
    fn analytics_compute_over_stored_rows() {
        // The store's read path feeds the pure analytics module end to end.
        let s = store();
        let r = seed_repo(&s, "r1");
        s.save_commits(
            &r,
            &[
                commit_at(&r, "aaa", "2026-06-01T09:00:00Z"),
                commit_at(&r, "bbb", "2026-06-01T18:00:00Z"),
                commit_at(&r, "ccc", "2026-06-04T09:00:00Z"),
            ],
        )
        .unwrap();

        let commits = s.list_commits(None).unwrap();
        let a =
            gitstate_core::analytics::compute(&commits, &[], &[], 1, "2026-06-01", "2026-06-07");
        assert_eq!(a.totals.commits, 3);
        assert_eq!(a.totals.active_days, 2);
        assert_eq!(a.heatmap.len(), 7);
        assert_eq!(a.totals.test_touch_rate, 1.0);
    }

    #[test]
    fn weights_normalize() {
        let w = Weights::default_weights().normalized();
        let sum = w.shipped + w.review + w.effort + w.quality + w.ownership + w.durability;
        assert!((sum - 1.0).abs() < 1e-9);
    }
}
