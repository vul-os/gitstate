//! Embedded, forward-only migration runner. Numbered SQL files applied in
//! order and tracked in `schema_migrations`. No external migration tool.

use gitstate_core::{ids::now_rfc3339, Error, Result};
use rusqlite::Connection;

/// Ordered migrations: (version, name, sql).
const MIGRATIONS: &[(&str, &str, &str)] =
    &[("0001", "init", include_str!("../migrations/0001_init.sql"))];

/// Apply every pending migration to `conn`.
pub fn run(conn: &Connection) -> Result<()> {
    conn.execute_batch(
        "CREATE TABLE IF NOT EXISTS schema_migrations (
            version TEXT PRIMARY KEY, applied_at TEXT NOT NULL
         );",
    )
    .map_err(Error::storage)?;

    for (version, name, sql) in MIGRATIONS {
        let already: bool = conn
            .query_row(
                "SELECT 1 FROM schema_migrations WHERE version = ?1",
                [version],
                |_| Ok(true),
            )
            .optional_ok()?;
        if already {
            continue;
        }
        conn.execute_batch(sql)
            .map_err(|e| Error::Storage(format!("migration {version} ({name}) failed: {e}")))?;
        conn.execute(
            "INSERT INTO schema_migrations (version, applied_at) VALUES (?1, ?2)",
            rusqlite::params![version, now_rfc3339()],
        )
        .map_err(Error::storage)?;
    }
    Ok(())
}

/// Tiny helper so the presence check reads cleanly.
trait OptionalOk<T> {
    fn optional_ok(self) -> Result<bool>;
}
impl OptionalOk<bool> for rusqlite::Result<bool> {
    fn optional_ok(self) -> Result<bool> {
        match self {
            Ok(v) => Ok(v),
            Err(rusqlite::Error::QueryReturnedNoRows) => Ok(false),
            Err(e) => Err(Error::storage(e)),
        }
    }
}
