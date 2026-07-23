//! A lightweight SZZ heuristic: find bug-*fixing* commits, then blame the lines
//! they change in the parent revision to attribute the bug-*introducing*
//! commit + author. Bounded (≤1500 fixes, ≤40 files/fix) so it stays cheap.
//!
//! This is a heuristic, not ground truth: it flags likely introductions to feed
//! the (inverted) quality dimension, never to name-and-shame.

use crate::history::WalkOpts;
use crate::open::GitRepo;
use gitstate_core::{BugIntroduction, Error, Result};
use regex::Regex;
use std::collections::HashMap;

const MAX_FIXES: usize = 1500;
const MAX_FILES_PER_FIX: usize = 40;

fn fix_regex() -> Regex {
    // Common fix/bug signals in a commit summary.
    Regex::new(r"(?i)\b(fix(e[sd])?|bug|hotfix|regression|patch|resolve[sd]?)\b").unwrap()
}

/// Attribute likely bug introductions from fix commits.
pub fn szz_bug_intros(repo: &GitRepo, opts: &WalkOpts) -> Result<Vec<BugIntroduction>> {
    let g = repo.repo();
    let re = fix_regex();

    let mut revwalk = g.revwalk().map_err(Error::git)?;
    revwalk.set_sorting(git2::Sort::TIME).map_err(Error::git)?;
    if revwalk.push_head().is_err() {
        return Ok(vec![]);
    }

    // (author_email, introduced_sha, fix_sha) -> lines
    let mut edges: HashMap<(String, String, String), u32> = HashMap::new();
    let mut fixes_seen = 0usize;

    for oid in revwalk {
        if fixes_seen >= MAX_FIXES {
            break;
        }
        if opts.max_commits > 0 && fixes_seen >= opts.max_commits {
            break;
        }
        let oid = oid.map_err(Error::git)?;
        let fix = g.find_commit(oid).map_err(Error::git)?;
        if fix.parent_count() != 1 {
            continue; // skip merges + roots
        }
        let summary = fix.summary().unwrap_or("");
        if !re.is_match(summary) {
            continue;
        }
        fixes_seen += 1;
        let fix_sha = oid.to_string();

        let parent = fix.parent(0).map_err(Error::git)?;
        let parent_tree = parent.tree().map_err(Error::git)?;
        let fix_tree = fix.tree().map_err(Error::git)?;
        let diff = g
            .diff_tree_to_tree(Some(&parent_tree), Some(&fix_tree), None)
            .map_err(Error::git)?;

        let mut files_done = 0usize;
        for delta in diff.deltas() {
            if files_done >= MAX_FILES_PER_FIX {
                break;
            }
            let Some(old_path) = delta.old_file().path() else {
                continue;
            };
            files_done += 1;

            // Blame the parent revision of the changed file; the commits that
            // last touched its lines are the introduction candidates.
            let mut bopts = git2::BlameOptions::new();
            bopts.newest_commit(parent.id());
            let blame = match g.blame_file(old_path, Some(&mut bopts)) {
                Ok(b) => b,
                Err(_) => continue,
            };
            for i in 0..blame.len() {
                if let Some(hunk) = blame.get_index(i) {
                    let intro_sha = hunk.orig_commit_id().to_string();
                    if intro_sha == fix_sha {
                        continue;
                    }
                    let email = hunk.orig_signature().email().unwrap_or("").to_lowercase();
                    if email.is_empty() {
                        continue;
                    }
                    let lines = hunk.lines_in_hunk() as u32;
                    *edges
                        .entry((email, intro_sha, fix_sha.clone()))
                        .or_insert(0) += lines;
                }
            }
        }
    }

    let mut out: Vec<BugIntroduction> = edges
        .into_iter()
        .map(
            |((author_email, introduced_sha, fix_sha), lines)| BugIntroduction {
                author_email,
                introduced_sha,
                fix_sha,
                lines,
            },
        )
        .collect();
    out.sort_by(|a, b| b.lines.cmp(&a.lines));
    Ok(out)
}
