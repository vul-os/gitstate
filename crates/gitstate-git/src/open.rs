//! Opening repositories and reading top-level refs.

use gitstate_core::{Error, Result};
use std::path::Path;

/// A thin wrapper over a `git2::Repository`.
pub struct GitRepo {
    pub(crate) inner: git2::Repository,
}

impl GitRepo {
    pub(crate) fn repo(&self) -> &git2::Repository {
        &self.inner
    }
}

/// Open (discovering upward) the repository at `path`.
pub fn open_repo(path: &Path) -> Result<GitRepo> {
    let inner = git2::Repository::discover(path)
        .or_else(|_| git2::Repository::open(path))
        .map_err(|e| Error::git(format!("open {}: {e}", path.display())))?;
    Ok(GitRepo { inner })
}

/// The current HEAD commit sha, or `None` for an empty repository.
pub fn head_sha(repo: &GitRepo) -> Result<Option<String>> {
    match repo.repo().head() {
        Ok(h) => {
            let commit = h.peel_to_commit().map_err(Error::git)?;
            Ok(Some(commit.id().to_string()))
        }
        Err(e)
            if e.code() == git2::ErrorCode::UnbornBranch
                || e.code() == git2::ErrorCode::NotFound =>
        {
            Ok(None)
        }
        Err(e) => Err(Error::git(e)),
    }
}

/// The repository's default branch shorthand (best-effort; falls back to
/// "main").
pub fn default_branch(repo: &GitRepo) -> Result<String> {
    // Prefer the remote HEAD symbolic target (origin/HEAD -> origin/<branch>).
    if let Ok(reference) = repo.repo().find_reference("refs/remotes/origin/HEAD") {
        if let Some(target) = reference.symbolic_target() {
            if let Some(name) = target.rsplit('/').next() {
                return Ok(name.to_string());
            }
        }
    }
    // Otherwise the local HEAD shorthand, if on a branch.
    if let Ok(h) = repo.repo().head() {
        if h.is_branch() {
            if let Some(name) = h.shorthand() {
                return Ok(name.to_string());
            }
        }
    }
    Ok("main".to_string())
}
