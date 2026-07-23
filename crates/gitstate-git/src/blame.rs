//! Line-survival via `git blame` over the HEAD tree. Bounded to keep large
//! repositories responsive (≤4000 files, files ≤2 MiB).

use crate::history::WalkOpts;
use crate::open::GitRepo;
use gitstate_core::{AuthorSurvival, Error, Result};
use std::collections::HashMap;
use std::path::Path;

const MAX_FILES: usize = 4000;
const MAX_FILE_BYTES: usize = 2 * 1024 * 1024;

/// Count lines still present at HEAD per author. `authored_lines` is set equal
/// to `surviving_lines` here (blame only sees current lines); the durability
/// ratio in `derive_contributions` uses commit additions as the authored
/// denominator, so this field is a best-effort echo.
pub fn blame_survival(repo: &GitRepo, _opts: &WalkOpts) -> Result<Vec<AuthorSurvival>> {
    let g = repo.repo();
    let head = match g.head() {
        Ok(h) => h,
        Err(_) => return Ok(vec![]), // empty repo
    };
    let commit = head.peel_to_commit().map_err(Error::git)?;
    let tree = commit.tree().map_err(Error::git)?;

    // Collect blob paths (bounded).
    let mut paths: Vec<String> = Vec::new();
    tree.walk(git2::TreeWalkMode::PreOrder, |root, entry| {
        if paths.len() >= MAX_FILES {
            return git2::TreeWalkResult::Abort;
        }
        if entry.kind() == Some(git2::ObjectType::Blob) {
            if let Some(name) = entry.name() {
                paths.push(format!("{root}{name}"));
            }
        }
        git2::TreeWalkResult::Ok
    })
    .map_err(Error::git)?;

    let mut per_email: HashMap<String, u32> = HashMap::new();
    for path in paths {
        // Skip oversized blobs.
        if let Ok(entry) = tree.get_path(Path::new(&path)) {
            if let Ok(obj) = entry.to_object(g) {
                if let Some(blob) = obj.as_blob() {
                    if blob.size() > MAX_FILE_BYTES {
                        continue;
                    }
                }
            }
        }
        let mut opts = git2::BlameOptions::new();
        let blame = match g.blame_file(Path::new(&path), Some(&mut opts)) {
            Ok(b) => b,
            Err(_) => continue, // binary / unmergeable / gone
        };
        for i in 0..blame.len() {
            if let Some(hunk) = blame.get_index(i) {
                let lines = hunk.lines_in_hunk() as u32;
                let email = hunk.final_signature().email().unwrap_or("").to_lowercase();
                if !email.is_empty() {
                    *per_email.entry(email).or_insert(0) += lines;
                }
            }
        }
    }

    let mut out: Vec<AuthorSurvival> = per_email
        .into_iter()
        .map(|(author_email, surviving_lines)| AuthorSurvival {
            author_email,
            surviving_lines,
            authored_lines: surviving_lines,
        })
        .collect();
    out.sort_by(|a, b| b.surviving_lines.cmp(&a.surviving_lines));
    Ok(out)
}
