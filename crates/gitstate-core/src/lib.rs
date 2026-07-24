//! gitstate-core — the pure domain of gitstate.
//!
//! Derive true project state, effort, contribution (six gaming-resistant
//! dimensions), and classification directly from git + forge — locally.
//!
//! This crate performs **no I/O**: it holds the domain types, the four adapter
//! traits ([`traits::ForgeClient`], [`traits::Classifier`], [`traits::Store`],
//! [`traits::SyncEngine`]), the deterministic derivation helpers, and the
//! signed [`taxonomy::Taxonomy`]. Everything else (git2 walk, gh/glab shell,
//! rusqlite store, axum daemon, CLI, Tauri shell) is a thin adapter over these.

pub mod analytics;
pub mod derive;
pub mod domain;
pub mod error;
pub mod health;
pub mod ids;
pub mod taxonomy;
pub mod traits;

pub use error::{Error, Result};

pub use analytics::Analytics;

// The domain vocabulary is re-exported flat so adapter crates can
// `use gitstate_core::{Repo, WorkItem, ...}`.
pub use domain::{
    AuthorSurvival, BugIntroduction, CatField, Category, CategorySource, Classification, Commit,
    Context, ContextPrRef, Contribution, Contributor, CtxField, DiffSummary, DimensionRaw,
    Dimensions, EffortEstimate, EffortMethod, Forge, ProjectState, Repo, SyncOp, Weights, WorkItem,
    WorkKind, WorkState,
};
pub use ids::{
    now_rfc3339, now_wall_ms, CategoryId, ContextId, ContributorId, Hlc, PeerId, RepoId, WorkItemId,
};
pub use taxonomy::{Taxonomy, TaxonomyCategory, DEFAULT_TAXONOMY_PUBKEY};
pub use traits::{
    Classifier, ClassifierCapability, ForgeClient, MergeOutcome, Store, SyncEngine, SyncStatus,
};
