//! gitstate-git — the git2-rs derivation engine.
//!
//! Opens repositories, walks history, blames for line survival, runs a
//! lightweight SZZ, and summarizes diffs — then derives the whole-repo
//! [`gitstate_core::ProjectState`] and the six-dimension
//! [`gitstate_core::Contribution`]s. Git-only inputs; the forge snapshot is
//! passed in. No source is ever retained — only aggregates.

mod blame;
mod derive_contrib;
mod derive_state;
mod diff;
mod history;
mod open;
mod szz;
pub mod util;

pub use blame::blame_survival;
pub use derive_contrib::derive_contributions;
pub use derive_state::{derive_project_state, percentile};
pub use diff::{diff_summary, diff_summary_for_pr};
pub use history::{collect_contributors, walk_commits, WalkOpts};
pub use open::{default_branch, head_sha, open_repo, GitRepo};
pub use szz::szz_bug_intros;

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::RepoId;
    use std::path::Path;
    use std::process::Command;

    fn git(dir: &Path, args: &[&str]) {
        let ok = Command::new("git")
            .args(args)
            .current_dir(dir)
            .env("GIT_AUTHOR_NAME", "Ada")
            .env("GIT_AUTHOR_EMAIL", "ada@example.com")
            .env("GIT_COMMITTER_NAME", "Ada")
            .env("GIT_COMMITTER_EMAIL", "ada@example.com")
            .status()
            .expect("git runs")
            .success();
        assert!(ok, "git {args:?} failed");
    }

    #[test]
    fn walk_and_derive_on_a_real_repo() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        git(dir, &["init", "-q", "-b", "main"]);
        std::fs::write(dir.join("a.rs"), "fn main() {}\n").unwrap();
        git(dir, &["add", "."]);
        git(dir, &["commit", "-q", "-m", "Add main"]);
        std::fs::write(dir.join("a.rs"), "fn main() { println!(); }\n").unwrap();
        std::fs::write(dir.join("a_test.rs"), "#[test] fn t() {}\n").unwrap();
        git(dir, &["add", "."]);
        git(dir, &["commit", "-q", "-m", "Fix main and add test"]);

        let repo = open_repo(dir).unwrap();
        let repo_id = RepoId::new();
        assert!(head_sha(&repo).unwrap().is_some());
        assert_eq!(default_branch(&repo).unwrap(), "main");

        let opts = WalkOpts::default();
        let commits = walk_commits(&repo, &opts, &repo_id).unwrap();
        assert_eq!(commits.len(), 2);
        assert!(commits.iter().any(|c| c.is_test_touch));

        let survival = blame_survival(&repo, &opts).unwrap();
        assert!(survival.iter().any(|s| s.author_email == "ada@example.com"));

        let contributors = collect_contributors(&commits, &[]);
        assert!(!contributors.is_empty());

        let state = derive_project_state(&repo, &repo_id, &[]).unwrap();
        assert!(!state.head_sha.is_empty());
        assert!(!state.warnings.is_empty()); // no forge data

        let contribs = derive_contributions(
            &commits,
            &survival,
            &[],
            &[],
            &[],
            &contributors,
            &gitstate_core::Weights::default_weights(),
            "2000-01-01T00:00:00Z",
            "2100-01-01T00:00:00Z",
            &repo_id,
        )
        .unwrap();
        assert!(!contribs.is_empty());
    }
}
