//! Whole-repo project-state derivation (DORA-flavoured) from the git HEAD plus
//! a forge snapshot.

use crate::open::GitRepo;
use crate::util::hours_between;
use gitstate_core::ids::now_rfc3339;
use gitstate_core::{ProjectState, RepoId, Result, WorkItem, WorkKind, WorkState};

/// Derive [`ProjectState`] from HEAD and the forge work-item snapshot.
pub fn derive_project_state(
    repo: &GitRepo,
    repo_id: &RepoId,
    forge_items: &[WorkItem],
) -> Result<ProjectState> {
    let head_sha = crate::open::head_sha(repo)?.unwrap_or_default();

    let mut open_prs = 0u32;
    let mut merged_prs = 0u32;
    let mut draft_prs = 0u32;
    let mut open_issues = 0u32;
    let mut closed_issues = 0u32;
    let mut reverts = 0u32;
    let mut cycle_hours: Vec<f64> = Vec::new();

    for w in forge_items {
        match w.kind {
            WorkKind::Pr => match w.state {
                WorkState::Draft => draft_prs += 1,
                WorkState::Open | WorkState::InProgress => open_prs += 1,
                WorkState::Merged | WorkState::Done => {
                    merged_prs += 1;
                    if w.title.to_lowercase().starts_with("revert") {
                        reverts += 1;
                    }
                    if let Some(merged) = &w.merged_at {
                        if let Some(h) = hours_between(&w.created_at, merged) {
                            cycle_hours.push(h);
                        }
                    }
                }
                WorkState::Closed => {} // closed-unmerged PR: neither open nor done
            },
            WorkKind::Issue => match w.state {
                WorkState::Closed | WorkState::Done | WorkState::Merged => closed_issues += 1,
                _ => open_issues += 1,
            },
            _ => {}
        }
    }

    let in_progress = open_prs;
    let done = merged_prs + closed_issues;
    let change_failure_rate = if merged_prs > 0 {
        Some(reverts as f64 / merged_prs as f64)
    } else {
        None
    };

    let mut warnings = Vec::new();
    if forge_items.is_empty() {
        warnings.push(
            "no forge data — scanned git only; PR/issue/review metrics are unavailable".to_string(),
        );
    }
    if head_sha.is_empty() {
        warnings.push("repository has no commits (empty HEAD)".to_string());
    }

    Ok(ProjectState {
        repo_id: repo_id.clone(),
        head_sha,
        open_prs,
        merged_prs,
        draft_prs,
        open_issues,
        closed_issues,
        in_progress,
        done,
        cycle_time_p50_hours: percentile(&cycle_hours, 0.50),
        cycle_time_p90_hours: percentile(&cycle_hours, 0.90),
        change_failure_rate,
        computed_at: now_rfc3339(),
        warnings,
    })
}

/// Linear-interpolated percentile of a sample (`q` in 0..=1). `None` if empty.
pub fn percentile(values: &[f64], q: f64) -> Option<f64> {
    if values.is_empty() {
        return None;
    }
    let mut v: Vec<f64> = values.iter().copied().filter(|x| x.is_finite()).collect();
    if v.is_empty() {
        return None;
    }
    v.sort_by(|a, b| a.partial_cmp(b).unwrap());
    if v.len() == 1 {
        return Some(v[0]);
    }
    let rank = q.clamp(0.0, 1.0) * (v.len() - 1) as f64;
    let lo = rank.floor() as usize;
    let hi = rank.ceil() as usize;
    let frac = rank - lo as f64;
    Some(v[lo] + (v[hi] - v[lo]) * frac)
}
