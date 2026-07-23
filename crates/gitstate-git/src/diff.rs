//! Diff shape extraction for effort judging. Emits the *shape* of a change
//! (churn, files, languages, paths) — never the source itself.

use crate::open::GitRepo;
use crate::util::ext_to_lang;
use gitstate_core::{DiffSummary, Error, Result, WorkItem, WorkItemId};

/// Summarize a single commit's diff against its first parent.
pub fn diff_summary(repo: &GitRepo, sha: &str) -> Result<DiffSummary> {
    let g = repo.repo();
    let obj = g
        .revparse_single(sha)
        .map_err(|e| Error::git(format!("rev {sha}: {e}")))?;
    let commit = obj.peel_to_commit().map_err(Error::git)?;
    let tree = commit.tree().map_err(Error::git)?;
    let parent_tree = match commit.parent(0) {
        Ok(p) => Some(p.tree().map_err(Error::git)?),
        Err(_) => None,
    };
    let diff = g
        .diff_tree_to_tree(parent_tree.as_ref(), Some(&tree), None)
        .map_err(Error::git)?;

    let title = commit.summary().unwrap_or("").to_string();
    let body = commit.body().unwrap_or("").to_string();

    summarize(
        diff,
        WorkItemId(sha.to_string()),
        sha.to_string(),
        title,
        body,
    )
}

/// Summarize the diff between two revisions for a PR work item.
pub fn diff_summary_for_pr(
    repo: &GitRepo,
    base: &str,
    head: &str,
    item: &WorkItem,
) -> Result<DiffSummary> {
    let g = repo.repo();
    let base_tree = g
        .revparse_single(base)
        .and_then(|o| o.peel_to_tree())
        .map_err(|e| Error::git(format!("base {base}: {e}")))?;
    let head_tree = g
        .revparse_single(head)
        .and_then(|o| o.peel_to_tree())
        .map_err(|e| Error::git(format!("head {head}: {e}")))?;
    let diff = g
        .diff_tree_to_tree(Some(&base_tree), Some(&head_tree), None)
        .map_err(Error::git)?;

    summarize(
        diff,
        item.id.clone(),
        item.external_ref.clone(),
        item.title.clone(),
        item.body.clone(),
    )
}

fn summarize(
    diff: git2::Diff,
    item_id: WorkItemId,
    external_ref: String,
    title: String,
    body: String,
) -> Result<DiffSummary> {
    let stats = diff.stats().map_err(Error::git)?;
    let mut langs: Vec<String> = Vec::new();
    let mut paths: Vec<String> = Vec::new();
    for delta in diff.deltas() {
        if let Some(p) = delta.new_file().path().and_then(|p| p.to_str()) {
            let p = p.to_string();
            if let Some(l) = ext_to_lang(&p) {
                if !langs.iter().any(|x| x == l) {
                    langs.push(l.to_string());
                }
            }
            if paths.len() < 200 {
                paths.push(p);
            }
        }
    }
    Ok(DiffSummary {
        item_id,
        external_ref,
        additions: stats.insertions() as u32,
        deletions: stats.deletions() as u32,
        files: stats.files_changed() as u32,
        languages: langs,
        touched_paths: paths,
        title,
        body,
    })
}
