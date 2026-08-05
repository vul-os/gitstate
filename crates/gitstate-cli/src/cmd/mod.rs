//! CLI command implementations. Each submodule handles one command group and
//! calls into `gitstate_daemon::ops` — the same operations the HTTP API uses.

pub mod agent;
pub mod category;
pub mod context;
pub mod insight;
pub mod misc;
pub mod repo;
pub mod seed;
pub mod serve;

use gitstate_core::RepoId;
use gitstate_daemon::{build_state_from_env, AppState};

/// Shared invocation context (global flags).
pub struct Ctx {
    pub json: bool,
}

/// Resolve whatever the user typed for a repo into its id.
///
/// A registered repo has a uuid, but nobody wants to paste one: accept the
/// full id, an unambiguous id prefix, or the slug (`demo-org/atlas-api`, or
/// just `atlas-api` when that is unique). Ambiguity is an error rather than a
/// silent pick — deriving the wrong repo's state quietly would be worse than
/// asking again.
pub fn resolve_repo(state: &AppState, needle: &str) -> anyhow::Result<RepoId> {
    let repos = state.store.list_repos()?;
    if repos.iter().any(|r| r.id.0 == needle) {
        return Ok(RepoId::from(needle.to_string()));
    }

    let lower = needle.to_ascii_lowercase();
    let mut hits: Vec<&gitstate_core::Repo> = repos
        .iter()
        .filter(|r| {
            let slug = r.slug.to_ascii_lowercase();
            r.id.0.starts_with(needle)
                || slug == lower
                || slug.rsplit('/').next().is_some_and(|name| name == lower)
        })
        .collect();
    hits.dedup_by(|a, b| a.id == b.id);

    match hits.as_slice() {
        [one] => Ok(one.id.clone()),
        [] => anyhow::bail!("no registered repo matches `{needle}` (try `gitstate repo list`)"),
        many => {
            let list = many
                .iter()
                .map(|r| format!("  {}  {}", r.id.0, r.slug))
                .collect::<Vec<_>>()
                .join("\n");
            anyhow::bail!("`{needle}` matches {} repos:\n{list}", many.len())
        }
    }
}

impl Ctx {
    /// Build the domain state (opens the DB, wires classifier + taxonomy).
    pub fn state(&self) -> anyhow::Result<AppState> {
        Ok(build_state_from_env()?)
    }

    /// Print a serializable value as pretty JSON.
    pub fn print_json<T: serde::Serialize>(&self, value: &T) -> anyhow::Result<()> {
        println!("{}", serde_json::to_string_pretty(value)?);
        Ok(())
    }
}
