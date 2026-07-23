//! Row <-> domain (de)serialization helpers shared by the store impl.

use gitstate_core::{Error, Hlc, Result};

/// Serialize a `Vec<String>`-ish list to a JSON array TEXT column.
pub fn json_str<T: serde::Serialize>(v: &T) -> String {
    serde_json::to_string(v).unwrap_or_else(|_| "[]".to_string())
}

/// Parse a JSON array TEXT column back into a typed value (default on error).
pub fn parse_json<T: serde::de::DeserializeOwned + Default>(s: &str) -> T {
    serde_json::from_str(s).unwrap_or_default()
}

/// Decode nullable Hlc TEXT.
pub fn dec_hlc(s: Option<String>) -> Result<Option<Hlc>> {
    match s {
        Some(s) if !s.is_empty() => Ok(Some(Hlc::decode(&s)?)),
        _ => Ok(None),
    }
}

/// A zero clock, used as the neutral floor when a doc has no recorded write.
pub fn zero_hlc() -> Hlc {
    Hlc {
        wall_ms: 0,
        counter: 0,
        peer: gitstate_core::PeerId(String::new()),
    }
}

/// Map a rusqlite error into the crate error type.
pub fn st(e: rusqlite::Error) -> Error {
    Error::storage(e)
}
