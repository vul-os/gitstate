//! gitstate-store — local, single-file persistence over rusqlite (SQLite).
//!
//! One database (`<data>/gitstate.db`, WAL) holds registered repos, the derived
//! caches (commits, work items, project state, contributions, effort,
//! classifications), the CRDT-backed contexts and categories, the sync op log,
//! and local personalization feedback. No source is ever stored — only
//! aggregates. See `docs/ARCHITECTURE.md` §9 for the schema.

mod migrations;
mod schema;
mod store_impl;

pub use store_impl::{db_path, SqliteStore};
