//! The six-dimension contribution derivation — the gitstate essence.
//!
//! Evidence in (commits, blame survival, SZZ bug intros, forge items, judged
//! effort), normalized *within the repo cohort* so no dimension is an absolute
//! leaderboard. The composite is a weighted texture, never a rank.

use crate::util::{contributor_owns_email, top_dir};
use gitstate_core::derive::{composite, durability_ratio, normalize_dim, quality_from};
use gitstate_core::{
    AuthorSurvival, BugIntroduction, Commit, Contribution, Contributor, DimensionRaw, Dimensions,
    EffortEstimate, RepoId, Result, Weights, WorkItem, WorkKind, WorkState,
};
use std::collections::{HashMap, HashSet};

/// Derive one [`Contribution`] per active contributor over `[from, to]`.
#[allow(clippy::too_many_arguments)]
pub fn derive_contributions(
    commits: &[Commit],
    survival: &[AuthorSurvival],
    bug_intros: &[BugIntroduction],
    forge_items: &[WorkItem],
    effort: &[EffortEstimate],
    contributors: &[Contributor],
    weights: &Weights,
    from: &str,
    to: &str,
    repo_id: &RepoId,
) -> Result<Vec<Contribution>> {
    let n = contributors.len();
    if n == 0 {
        return Ok(vec![]);
    }

    // Per-contributor accumulators (indexed parallel to `contributors`).
    let mut raw = vec![DimensionRaw::default(); n];
    let mut areas: Vec<HashSet<String>> = vec![HashSet::new(); n];

    // Lookups.
    let effort_by_item: HashMap<&str, f64> = effort
        .iter()
        .map(|e| (e.item_id.0.as_str(), e.difficulty))
        .collect();
    let login_to_idx: HashMap<String, usize> = contributors
        .iter()
        .enumerate()
        .filter_map(|(i, c)| c.login.as_ref().map(|l| (l.to_lowercase(), i)))
        .collect();

    let idx_for_email = |email: &str| -> Option<usize> {
        contributors
            .iter()
            .position(|c| contributor_owns_email(c, email))
    };

    // Commits → authorship, agent/human split.
    for c in commits {
        if let Some(i) = idx_for_email(&c.author_email) {
            if contributors[i].is_agent {
                raw[i].agent_commits += 1;
            } else {
                raw[i].human_commits += 1;
            }
            raw[i].authored_lines = raw[i].authored_lines.saturating_add(c.additions);
        }
    }

    // Blame survival.
    for s in survival {
        if let Some(i) = idx_for_email(&s.author_email) {
            raw[i].surviving_lines = raw[i].surviving_lines.saturating_add(s.surviving_lines);
        }
    }

    // SZZ bug introductions (count of edges per author).
    for b in bug_intros {
        if let Some(i) = idx_for_email(&b.author_email) {
            raw[i].bug_intros += 1;
        }
    }

    // Forge items → shipped/review/effort/ownership.
    for w in forge_items {
        let Some(login) = w.author_login.as_deref() else {
            continue;
        };
        let Some(&i) = login_to_idx.get(&login.to_lowercase()) else {
            continue;
        };
        match w.kind {
            WorkKind::Pr => {
                if matches!(w.state, WorkState::Merged | WorkState::Done) {
                    raw[i].merged_prs += 1;
                    if w.title.to_lowercase().starts_with("revert") {
                        raw[i].reverts_caused += 1;
                    }
                }
                if let Some(d) = effort_by_item.get(w.id.0.as_str()) {
                    raw[i].effort_points += d;
                }
            }
            WorkKind::Issue => {
                if matches!(
                    w.state,
                    WorkState::Closed | WorkState::Done | WorkState::Merged
                ) {
                    raw[i].closed_issues += 1;
                }
            }
            WorkKind::Review => {
                raw[i].reviews_done += 1;
            }
            WorkKind::Commit => {}
        }
        for f in &w.files_touched {
            areas[i].insert(top_dir(f));
        }
    }

    for (i, a) in areas.iter().enumerate() {
        raw[i].areas_owned = a.len() as u32;
    }

    // Build cohorts for normalization.
    let shipped_raw: Vec<f64> = raw
        .iter()
        .map(|r| r.merged_prs as f64 + r.closed_issues as f64)
        .collect();
    let review_raw: Vec<f64> = raw.iter().map(|r| r.reviews_done as f64).collect();
    let effort_raw: Vec<f64> = raw.iter().map(|r| r.effort_points).collect();
    let quality_raw: Vec<f64> = raw
        .iter()
        .map(|r| quality_from(r.reverts_caused, r.bug_intros, None))
        .collect();
    let ownership_raw: Vec<f64> = raw.iter().map(|r| r.areas_owned as f64).collect();
    let durability_raw: Vec<f64> = raw
        .iter()
        .map(|r| durability_ratio(r.surviving_lines, r.authored_lines))
        .collect();

    let mut out = Vec::new();
    for i in 0..n {
        // Gate: skip contributors with no evidence in this window.
        let commits_total = raw[i].human_commits + raw[i].agent_commits;
        let active = commits_total > 0
            || raw[i].merged_prs > 0
            || raw[i].closed_issues > 0
            || raw[i].reviews_done > 0
            || raw[i].surviving_lines > 0;
        if !active {
            continue;
        }

        let dims = Dimensions {
            shipped: normalize_dim(shipped_raw[i], &shipped_raw),
            review: normalize_dim(review_raw[i], &review_raw),
            effort: normalize_dim(effort_raw[i], &effort_raw),
            quality: normalize_dim(quality_raw[i], &quality_raw),
            ownership: normalize_dim(ownership_raw[i], &ownership_raw),
            durability: normalize_dim(durability_raw[i], &durability_raw),
        };
        let composite_score = composite(&dims, weights);
        let agent_pct = if commits_total > 0 {
            raw[i].agent_commits as f64 / commits_total as f64
        } else {
            0.0
        };

        out.push(Contribution {
            contributor_id: contributors[i].id.clone(),
            repo_id: repo_id.clone(),
            from: from.to_string(),
            to: to.to_string(),
            dimensions: dims,
            raw: raw[i],
            agent_pct,
            composite: composite_score,
        });
    }
    Ok(out)
}
