//! The one error type shared across every gitstate crate.
//!
//! Libraries return `Result<T> = Result<T, Error>`; adapter crates (git,
//! forge, store, classify) map their foreign errors into these variants with
//! the convenience constructors so `?` stays ergonomic without pulling I/O
//! crates into `gitstate-core`.

/// Crate-wide result alias. Every fallible domain operation uses this.
pub type Result<T> = std::result::Result<T, Error>;

/// The single error enum returned across the whole workspace.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("{entity} not found: {id}")]
    NotFound { entity: &'static str, id: String },

    #[error("invalid value: {0}")]
    Invalid(String),

    #[error("storage error: {0}")]
    Storage(String),

    #[error("git error: {0}")]
    Git(String),

    #[error("forge error: {0}")]
    Forge(String),

    #[error("forge CLI not found: {0} (install it, or set a token for the REST path)")]
    ForgeCliMissing(String),

    #[error("classifier error: {0}")]
    Classify(String),

    #[error("http error: {0}")]
    Http(String),

    #[error("taxonomy is untrusted: {0}")]
    TaxonomyUntrusted(String),

    #[error("sync is disabled (build with --features sync-dmtap)")]
    SyncDisabled,

    #[error("io error: {0}")]
    Io(String),

    #[error("json error: {0}")]
    Json(#[from] serde_json::Error),
}

impl Error {
    pub fn invalid(msg: impl Into<String>) -> Self {
        Error::Invalid(msg.into())
    }
    pub fn storage(msg: impl std::fmt::Display) -> Self {
        Error::Storage(msg.to_string())
    }
    pub fn git(msg: impl std::fmt::Display) -> Self {
        Error::Git(msg.to_string())
    }
    pub fn forge(msg: impl std::fmt::Display) -> Self {
        Error::Forge(msg.to_string())
    }
    pub fn classify(msg: impl std::fmt::Display) -> Self {
        Error::Classify(msg.to_string())
    }
    pub fn http(msg: impl std::fmt::Display) -> Self {
        Error::Http(msg.to_string())
    }
    pub fn not_found(entity: &'static str, id: impl Into<String>) -> Self {
        Error::NotFound {
            entity,
            id: id.into(),
        }
    }
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Error::Io(e.to_string())
    }
}
