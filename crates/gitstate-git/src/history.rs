//! Commit-history walking and contributor collection.

use crate::open::GitRepo;
use crate::util::{detect_agent, epoch_to_rfc3339, rfc3339_to_epoch};
use gitstate_core::derive::is_test_path;
use gitstate_core::{Commit, Contributor, ContributorId, Error, RepoId, Result, WorkItem};

/// Bounds on a history walk.
#[derive(Debug, Clone)]
pub struct WalkOpts {
    pub max_commits: usize,
    pub since: Option<String>,
    pub branch: Option<String>,
}

impl Default for WalkOpts {
    fn default() -> Self {
        WalkOpts {
            max_commits: 50_000,
            since: None,
            branch: None,
        }
    }
}

/// Walk commit history from HEAD (or `opts.branch`), producing per-commit
/// derived aggregates. No source is retained — only counts and the summary.
pub fn walk_commits(repo: &GitRepo, opts: &WalkOpts, repo_id: &RepoId) -> Result<Vec<Commit>> {
    let g = repo.repo();
    let mut revwalk = g.revwalk().map_err(Error::git)?;
    revwalk.set_sorting(git2::Sort::TIME).map_err(Error::git)?;

    match &opts.branch {
        Some(b) => {
            let obj = g
                .revparse_single(b)
                .map_err(|e| Error::git(format!("branch {b}: {e}")))?;
            revwalk.push(obj.id()).map_err(Error::git)?;
        }
        None => {
            if revwalk.push_head().is_err() {
                return Ok(vec![]); // empty repo
            }
        }
    }

    let since_epoch = opts.since.as_deref().and_then(rfc3339_to_epoch);
    let mut out = Vec::new();

    for oid in revwalk {
        if out.len() >= opts.max_commits {
            break;
        }
        let oid = oid.map_err(Error::git)?;
        let commit = g.find_commit(oid).map_err(Error::git)?;
        let when = commit.time().seconds();
        if let Some(floor) = since_epoch {
            if when < floor {
                continue;
            }
        }

        let author = commit.author();
        let is_merge = commit.parent_count() > 1;

        let (additions, deletions, files_changed, is_test_touch) = if is_merge {
            (0, 0, 0, false)
        } else {
            commit_diff_stats(g, &commit)?
        };

        let summary = commit.summary().unwrap_or("").to_string();
        out.push(Commit {
            sha: oid.to_string(),
            repo_id: repo_id.clone(),
            author_email: author.email().unwrap_or("").to_string(),
            author_name: author.name().unwrap_or("").to_string(),
            committed_at: epoch_to_rfc3339(when),
            additions,
            deletions,
            files_changed,
            is_merge,
            is_test_touch,
            summary,
        });
    }
    Ok(out)
}

/// Diff a non-merge commit against its first parent (or the empty tree for a
/// root commit) and return (additions, deletions, files_changed, test_touch).
fn commit_diff_stats(g: &git2::Repository, commit: &git2::Commit) -> Result<(u32, u32, u32, bool)> {
    let tree = commit.tree().map_err(Error::git)?;
    let parent_tree = match commit.parent(0) {
        Ok(p) => Some(p.tree().map_err(Error::git)?),
        Err(_) => None,
    };
    let diff = g
        .diff_tree_to_tree(parent_tree.as_ref(), Some(&tree), None)
        .map_err(Error::git)?;

    let stats = diff.stats().map_err(Error::git)?;
    let mut test_touch = false;
    for delta in diff.deltas() {
        if let Some(p) = delta.new_file().path().and_then(|p| p.to_str()) {
            if is_test_path(p) {
                test_touch = true;
                break;
            }
        }
    }
    Ok((
        stats.insertions() as u32,
        stats.deletions() as u32,
        stats.files_changed() as u32,
        test_touch,
    ))
}

/// Build the contributor set from commit authors and forge item authors.
/// Emails become identities; forge logins are attached and, where a `noreply`
/// email encodes the login, cluster naturally via `merge_contributor_identities`.
pub fn collect_contributors(commits: &[Commit], forge_items: &[WorkItem]) -> Vec<Contributor> {
    use std::collections::HashMap;
    let mut by_email: HashMap<String, Contributor> = HashMap::new();

    for c in commits {
        let email = c.author_email.trim().to_lowercase();
        if email.is_empty() {
            continue;
        }
        let entry = by_email.entry(email.clone()).or_insert_with(|| {
            let kind = detect_agent(&c.author_name, &c.author_email, None);
            Contributor {
                id: ContributorId::new(),
                display_name: if c.author_name.is_empty() {
                    email.clone()
                } else {
                    c.author_name.clone()
                },
                primary_email: c.author_email.clone(),
                emails: vec![c.author_email.clone()],
                login: login_from_noreply(&c.author_email),
                is_agent: kind.is_some(),
                agent_kind: kind.map(|s| s.to_string()),
            }
        });
        if entry.display_name.is_empty() && !c.author_name.is_empty() {
            entry.display_name = c.author_name.clone();
        }
    }

    // Ensure every forge login has an identity; synthesize a noreply email so
    // the union-find in core can cluster it with the matching commit author.
    for w in forge_items {
        let Some(login) = w.author_login.as_deref() else {
            continue;
        };
        if login.is_empty() {
            continue;
        }
        let synth = format!("{}@users.noreply.github.com", login.to_lowercase());
        let already = by_email
            .values()
            .any(|c| c.login.as_deref() == Some(login) || super_owns(c, &synth));
        if already {
            continue;
        }
        let kind = detect_agent(login, &synth, Some(login));
        by_email
            .entry(synth.clone())
            .or_insert_with(|| Contributor {
                id: ContributorId::new(),
                display_name: login.to_string(),
                primary_email: synth.clone(),
                emails: vec![synth.clone()],
                login: Some(login.to_string()),
                is_agent: kind.is_some(),
                agent_kind: kind.map(|s| s.to_string()),
            });
    }

    let rows: Vec<Contributor> = by_email.into_values().collect();
    gitstate_core::derive::merge_contributor_identities(&rows)
}

fn super_owns(c: &Contributor, email: &str) -> bool {
    c.primary_email.eq_ignore_ascii_case(email)
        || c.emails.iter().any(|e| e.eq_ignore_ascii_case(email))
}

/// GitHub noreply emails encode the login: `12345+login@users.noreply...` or
/// `login@users.noreply...`.
fn login_from_noreply(email: &str) -> Option<String> {
    let e = email.to_lowercase();
    if !e.contains("users.noreply.github.com") {
        return None;
    }
    let local = e.split('@').next()?;
    let login = local.split_once('+').map(|(_, l)| l).unwrap_or(local);
    if login.is_empty() {
        None
    } else {
        Some(login.to_string())
    }
}
