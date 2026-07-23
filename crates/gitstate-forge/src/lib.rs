//! gitstate-forge — the [`ForgeClient`] seam for GitHub and GitLab.
//!
//! Both clients prefer the local `gh` / `glab` CLI (invoked with `--json` /
//! `-F json`), falling back to the REST/GraphQL API only when the CLI is absent
//! and a token is configured. A missing CLI with no token yields the typed
//! [`gitstate_core::Error::ForgeCliMissing`]. A plain scan of a `Local` repo
//! never touches this crate, so it performs no network I/O uninvited.

mod cli;
mod github;
mod gitlab;
mod rest;

pub use cli::{detect_forge, slug_from_remote};
pub use github::GitHubClient;
pub use gitlab::GitLabClient;

use gitstate_core::{Forge, ForgeClient};

/// Factory: the right client for a forge, configured from the environment.
/// `Local` maps to a GitHub client (unused for local scans) so the daemon can
/// hold a uniform registry; callers should not call it for `Local` repos.
pub fn for_forge(forge: Forge) -> Box<dyn ForgeClient> {
    match forge {
        Forge::GitLab => Box::new(GitLabClient::from_env()),
        Forge::GitHub | Forge::Local => Box::new(GitHubClient::from_env()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn factory_matches_forge() {
        assert_eq!(for_forge(Forge::GitHub).forge(), Forge::GitHub);
        assert_eq!(for_forge(Forge::GitLab).forge(), Forge::GitLab);
    }
}
