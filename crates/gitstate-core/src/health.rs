//! Pure, deterministic engineering-health and involvement analytics.
//!
//! [`analytics`](crate::analytics) answers "what happened" (raw activity,
//! throughput, cycle time). This module answers the harder, more opinionated
//! questions a lead actually loses sleep over: are we shipping safely (DORA),
//! is the project one resignation away from trouble (bus factor), is review
//! actually happening (review health), is the work getting sloppier (quality
//! proxies), and who is really carrying which repo (involvement). Same rules
//! as `analytics`: no I/O, every ratio guards its own denominator so nothing
//! here ever produces `NaN`/`Inf`, and every ordering is fully deterministic
//! (never a `HashMap` iteration order leaking into a `Vec`).
//!
//! We have no CD/deploy signal locally (no webhook, no pipeline log), so
//! [`dora`]'s `deploy_proxy_per_week` is explicitly a proxy — merge commits
//! per week — not a real deploy count. It is documented as such everywhere
//! it appears so a caller never mistakes it for ground truth.

use std::collections::{HashMap, HashSet};

use serde::{Deserialize, Serialize};
use time::Date;

use crate::analytics::{self, day_key, percentile};
use crate::domain::{
    Commit, Contribution, Contributor, DimensionRaw, Dimensions, Weights, WorkItem, WorkKind,
    WorkState,
};

/// A commit touching more than this many changed lines (additions +
/// deletions) counts as "large" for [`Quality::large_commit_share`]. Chosen
/// as a round, conservative cutoff — comfortably past a typical reviewable
/// diff, short of a vendor-drop/generated-file outlier skewing the whole
/// metric on its own.
const LARGE_COMMIT_LINES: u32 = 400;

// ─────────────────────────────── shapes ───────────────────────────────

/// DORA-flavoured delivery metrics over `[from, to]`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Dora {
    pub cycle_p50_hours: Option<f64>,
    pub cycle_p90_hours: Option<f64>,
    /// 0..1 share of merged PRs whose title/labels read as a revert, hotfix,
    /// or rollback. `None` when there were no merged PRs to judge.
    pub change_failure_rate: Option<f64>,
    pub merge_frequency_per_week: f64,
    /// Merge commits per week — a **local stand-in for deploy frequency**.
    /// gitstate has no CD/pipeline signal, so this counts `Commit.is_merge`
    /// instead. It correlates with delivery cadence but is not a deploy
    /// count; never present it as one.
    pub deploy_proxy_per_week: f64,
    /// Merged PRs that actually produced a valid (non-negative,
    /// fully-parsable) lead-time sample — the population the percentiles
    /// above were computed over.
    pub lead_time_samples: u32,
}

/// The classic bus-factor readout: how many people you'd need to lose before
/// more than half the recent commit history walks out the door with them.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct BusFactor {
    /// Minimum number of contributors whose combined commit share first
    /// reaches or exceeds 50%. `0` when there are no commits in range.
    pub count: u32,
    /// The single largest contributor's share of commits, 0..1.
    pub top_share: f64,
    /// All contributors, ordered by share desc then email asc.
    pub contributors: Vec<OwnershipShare>,
}

/// One identity's share of a commit population (a repo, a range, whatever
/// the caller scoped it to).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct OwnershipShare {
    pub email: String,
    pub name: String,
    pub commits: u32,
    /// 0..1 share of the population this share was computed against.
    pub share: f64,
    pub is_agent: bool,
}

/// Is anyone actually reviewing merged work?
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ReviewHealth {
    pub merged_prs: u32,
    /// Total `WorkKind::Review` items, independent of whether they matched a
    /// known PR.
    pub reviews_done: u32,
    /// 0..1 share of merged PRs that have at least one matching review.
    /// `0.0` (never `NaN`) when there are no merged PRs.
    pub reviewed_pr_share: f64,
    pub unreviewed_merged: u32,
}

/// Cheap, gameable-but-useful quality proxies from commit shape alone.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Quality {
    /// 0..1 share of commits that touched a test path.
    pub test_touch_rate: f64,
    pub avg_commit_size_lines: f64,
    /// 0..1 share of commits over [`LARGE_COMMIT_LINES`] changed lines.
    pub large_commit_share: f64,
    /// Commits whose summary reads as a revert (case-insensitive substring
    /// match on "revert").
    pub revert_commits: u32,
}

/// Which repos a set of people actually touch, and which people carry each
/// repo — the same join read from both directions so a caller never has to
/// build the second view itself.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Involvement {
    /// Ordered by commits desc then `repo_id` asc.
    pub repos: Vec<RepoInvolvement>,
    /// Ordered by `total_commits` desc then email asc.
    pub people: Vec<PersonInvolvement>,
}

/// One repo's activity and who is carrying it.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RepoInvolvement {
    pub repo_id: String,
    pub slug: String,
    pub commits: u32,
    /// Ordered by share desc then email asc. `share` is of this repo's own
    /// commit total.
    pub contributors: Vec<OwnershipShare>,
}

/// One person's footprint across every repo they touched in range.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PersonInvolvement {
    pub email: String,
    pub name: String,
    pub is_agent: bool,
    pub total_commits: u32,
    /// Distinct repos this person committed to (that were also present in
    /// the `repos` scope passed to [`involvement`]).
    pub repo_count: u32,
    /// Ordered by commits desc then `repo_id` asc.
    pub repos: Vec<PersonRepoShare>,
}

/// One person's activity in one repo.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PersonRepoShare {
    pub repo_id: String,
    pub slug: String,
    pub commits: u32,
    /// 0..1 share of this *person's own* total commits, not the repo's.
    pub share: f64,
}

/// The composed engineering-health payload for a range.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EngHealth {
    pub range: analytics::Range,
    pub dora: Dora,
    pub bus_factor: BusFactor,
    pub review: ReviewHealth,
    pub quality: Quality,
}

// ─────────────────────────── date helpers ───────────────────────────

// Mirrors `analytics::parse_date`, which is private to that module — kept
// tiny and local rather than exposing internal parsing across modules.
fn parse_date(day: &str) -> Option<Date> {
    let mut it = day.splitn(3, '-');
    let y: i32 = it.next()?.parse().ok()?;
    let m: u8 = it.next()?.parse().ok()?;
    let d: u8 = it.next()?.parse().ok()?;
    Date::from_calendar_date(y, time::Month::try_from(m).ok()?, d).ok()
}

/// Number of whole days in `[from, to]` inclusive, or `0` for an unparsable
/// or inverted range (never negative, never a panic).
fn range_days(from: &str, to: &str) -> u32 {
    match (parse_date(from), parse_date(to)) {
        (Some(a), Some(b)) if b >= a => ((b - a).whole_days() + 1) as u32,
        _ => 0,
    }
}

/// Weeks in `[from, to]`, as a continuous fraction so short ranges still
/// produce a meaningful weekly rate. `0.0` for an unparsable/inverted range —
/// callers must treat that as "no denominator" rather than divide by it.
fn weeks_in_range(from: &str, to: &str) -> f64 {
    range_days(from, to) as f64 / 7.0
}

fn in_range(ts: &str, from: &str, to: &str) -> bool {
    day_key(ts).is_some_and(|d| d >= from && d <= to)
}

/// Case-insensitive: does this text read as a revert/hotfix/rollback?
fn looks_like_incident(s: &str) -> bool {
    let l = s.to_lowercase();
    l.contains("revert")
        || l.contains("hotfix")
        || l.contains("rollback")
        || l.contains("roll back")
}

/// email (lowercased) -> the merged identity that owns it. Shared by
/// [`bus_factor`] and [`involvement`] so alias folding is identical in both.
fn identity_map(known: &[Contributor]) -> HashMap<String, &Contributor> {
    let mut by_email: HashMap<String, &Contributor> = HashMap::new();
    for c in known {
        for e in std::iter::once(&c.primary_email).chain(c.emails.iter()) {
            by_email.insert(e.trim().to_lowercase(), c);
        }
    }
    by_email
}

/// Resolve a commit author to its folded identity key + display name + agent
/// flag, using `known` merged-identity records when the email matches one.
fn resolve_identity(
    by_email: &HashMap<String, &Contributor>,
    author_email: &str,
    author_name: &str,
) -> (String, String, bool) {
    let email = author_email.trim().to_lowercase();
    let identity = by_email.get(&email);
    let key = identity
        .map(|i| i.primary_email.trim().to_lowercase())
        .unwrap_or_else(|| email.clone());
    let name = identity
        .map(|i| i.display_name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| author_name.to_string());
    let is_agent = identity.map(|i| i.is_agent).unwrap_or(false);
    (key, name, is_agent)
}

// ─────────────────────────── computations ───────────────────────────

/// DORA-flavoured delivery metrics. See [`Dora`] for field-by-field caveats,
/// in particular that `deploy_proxy_per_week` is a merge-commit proxy, not a
/// real deploy count.
pub fn dora(items: &[WorkItem], commits: &[Commit], from: &str, to: &str) -> Dora {
    let cycles = analytics::cycle_times(items, from, to);
    let mut hours: Vec<f64> = cycles.iter().map(|c| c.hours).collect();
    hours.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));

    let merged: Vec<&WorkItem> = items
        .iter()
        .filter(|w| {
            w.kind == WorkKind::Pr
                && w.state == WorkState::Merged
                && w.merged_at
                    .as_deref()
                    .is_some_and(|m| in_range(m, from, to))
        })
        .collect();

    let change_failure_rate = if merged.is_empty() {
        None
    } else {
        let bad = merged
            .iter()
            .filter(|w| {
                looks_like_incident(&w.title) || w.labels.iter().any(|l| looks_like_incident(l))
            })
            .count();
        Some(bad as f64 / merged.len() as f64)
    };

    let weeks = weeks_in_range(from, to);
    let merge_frequency_per_week = if weeks > 0.0 {
        merged.len() as f64 / weeks
    } else {
        0.0
    };

    let merge_commits = commits
        .iter()
        .filter(|c| c.is_merge && in_range(&c.committed_at, from, to))
        .count();
    let deploy_proxy_per_week = if weeks > 0.0 {
        merge_commits as f64 / weeks
    } else {
        0.0
    };

    Dora {
        cycle_p50_hours: percentile(&hours, 0.5),
        cycle_p90_hours: percentile(&hours, 0.9),
        change_failure_rate,
        merge_frequency_per_week,
        deploy_proxy_per_week,
        lead_time_samples: cycles.len() as u32,
    }
}

/// Classic bus factor: fewest contributors whose combined commit share
/// reaches 50%, plus the full ownership breakdown it was computed from.
/// Merged identity aliases (`Contributor.emails`) fold into one row, same as
/// [`analytics::contributor_stats`].
pub fn bus_factor(commits: &[Commit], known: &[Contributor], from: &str, to: &str) -> BusFactor {
    let by_email = identity_map(known);

    struct Acc {
        name: String,
        commits: u32,
        is_agent: bool,
    }

    let mut acc: HashMap<String, Acc> = HashMap::new();
    let mut total: u32 = 0;
    for c in commits {
        if !in_range(&c.committed_at, from, to) {
            continue;
        }
        let (key, name, is_agent) = resolve_identity(&by_email, &c.author_email, &c.author_name);
        let e = acc.entry(key).or_insert_with(|| Acc {
            name,
            commits: 0,
            is_agent,
        });
        e.commits += 1;
        total += 1;
    }

    let mut contributors: Vec<OwnershipShare> = acc
        .into_iter()
        .map(|(email, a)| OwnershipShare {
            email,
            name: a.name,
            commits: a.commits,
            share: ratio(a.commits, total),
            is_agent: a.is_agent,
        })
        .collect();
    contributors.sort_by(|a, b| {
        b.share
            .partial_cmp(&a.share)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then_with(|| a.email.cmp(&b.email))
    });

    let top_share = contributors.first().map(|c| c.share).unwrap_or(0.0);

    let mut count = 0u32;
    let mut cumulative = 0.0;
    for c in &contributors {
        cumulative += c.share;
        count += 1;
        if cumulative >= 0.5 {
            break;
        }
    }
    if contributors.is_empty() {
        count = 0;
    }

    BusFactor {
        count,
        top_share,
        contributors,
    }
}

/// `"#42-review-1"` → `Some("#42")`.
///
/// Reviews are not keyed by their PR's ref: `gitstate-forge`'s `list_reviews`
/// materializes each one as `#{pr}-review-{n}` so several reviews on the same
/// PR stay distinct rows. The PR key is therefore the prefix before that
/// marker — comparing the refs directly (as an earlier version did) matched
/// nothing and reported every merged PR as unreviewed.
fn pr_ref_of_review(external_ref: &str) -> Option<&str> {
    external_ref
        .find("-review-")
        .map(|i| &external_ref[..i])
        .filter(|prefix| !prefix.is_empty())
}

/// Are merged PRs actually getting reviewed?
///
/// Matching is keyed on **(repo, pr ref)**, not the ref alone. PR numbers
/// restart per repo, so a repo-blind match lets a review of `#7` in one repo
/// satisfy an entirely unrelated `#7` in another — which across a handful of
/// repos silently reports almost everything as reviewed.
pub fn review_health(items: &[WorkItem]) -> ReviewHealth {
    let merged: Vec<(&str, &str)> = items
        .iter()
        .filter(|w| w.kind == WorkKind::Pr && w.state == WorkState::Merged)
        .map(|w| (w.repo_id.0.as_str(), w.external_ref.as_str()))
        .collect();
    let merged_prs = merged.len() as u32;

    let reviews_done = items.iter().filter(|w| w.kind == WorkKind::Review).count() as u32;
    let reviewed: HashSet<(&str, &str)> = items
        .iter()
        .filter(|w| w.kind == WorkKind::Review)
        .filter_map(|w| pr_ref_of_review(&w.external_ref).map(|r| (w.repo_id.0.as_str(), r)))
        .collect();

    // Counting over the merged PRs themselves means a review pointing at a PR
    // we don't have (or at a still-open one) can never push the share above 1.
    let reviewed_merged = merged.iter().filter(|k| reviewed.contains(*k)).count() as u32;

    ReviewHealth {
        merged_prs,
        reviews_done,
        reviewed_pr_share: ratio(reviewed_merged, merged_prs),
        unreviewed_merged: merged_prs - reviewed_merged,
    }
}

/// Cheap, commit-shape-only quality proxies. See [`Quality`] and
/// [`LARGE_COMMIT_LINES`] for the specifics each field measures.
///
/// `items` is accepted for signature parity with the other range-scoped
/// health functions (every quality signal here is derivable from commit
/// shape alone); call [`review_health`] for the review-linked view.
pub fn quality(commits: &[Commit], _items: &[WorkItem], from: &str, to: &str) -> Quality {
    let scoped: Vec<&Commit> = commits
        .iter()
        .filter(|c| in_range(&c.committed_at, from, to))
        .collect();
    let n = scoped.len() as u32;

    let test_touch = scoped.iter().filter(|c| c.is_test_touch).count() as u32;
    let total_lines: u64 = scoped
        .iter()
        .map(|c| (c.additions as u64) + (c.deletions as u64))
        .sum();
    let large = scoped
        .iter()
        .filter(|c| c.additions + c.deletions > LARGE_COMMIT_LINES)
        .count() as u32;
    let reverts = scoped
        .iter()
        .filter(|c| c.summary.to_lowercase().contains("revert"))
        .count() as u32;

    Quality {
        test_touch_rate: ratio(test_touch, n),
        avg_commit_size_lines: if n == 0 {
            0.0
        } else {
            total_lines as f64 / n as f64
        },
        large_commit_share: ratio(large, n),
        revert_commits: reverts,
    }
}

/// Who touches which repos, and which repos each person carries — the same
/// commit population joined from both directions. `repos` is the `(repo_id,
/// slug)` scope: commits whose `repo_id` isn't in it are counted toward a
/// person's `total_commits` but contribute no `RepoInvolvement`/
/// `PersonRepoShare` entry (there is no slug to label it with).
pub fn involvement(
    commits: &[Commit],
    repos: &[(String, String)],
    known: &[Contributor],
    from: &str,
    to: &str,
) -> Involvement {
    let by_email = identity_map(known);
    let slug_of: HashMap<&str, &str> = repos
        .iter()
        .map(|(id, slug)| (id.as_str(), slug.as_str()))
        .collect();

    struct PersonAcc {
        name: String,
        commits: u32,
        is_agent: bool,
    }
    struct PersonTotal {
        name: String,
        is_agent: bool,
        total: u32,
        per_repo: HashMap<String, u32>,
    }

    let mut repo_commit_counts: HashMap<String, u32> = HashMap::new();
    let mut per_repo_people: HashMap<String, HashMap<String, PersonAcc>> = HashMap::new();
    let mut person_totals: HashMap<String, PersonTotal> = HashMap::new();

    for c in commits {
        if !in_range(&c.committed_at, from, to) {
            continue;
        }
        let repo_id = c.repo_id.as_ref().to_string();
        let (key, name, is_agent) = resolve_identity(&by_email, &c.author_email, &c.author_name);

        *repo_commit_counts.entry(repo_id.clone()).or_insert(0) += 1;

        let repo_people = per_repo_people.entry(repo_id.clone()).or_default();
        let pa = repo_people.entry(key.clone()).or_insert_with(|| PersonAcc {
            name: name.clone(),
            commits: 0,
            is_agent,
        });
        pa.commits += 1;

        let pt = person_totals.entry(key).or_insert_with(|| PersonTotal {
            name,
            is_agent,
            total: 0,
            per_repo: HashMap::new(),
        });
        pt.total += 1;
        *pt.per_repo.entry(repo_id).or_insert(0) += 1;
    }

    let mut repos_out: Vec<RepoInvolvement> = repos
        .iter()
        .map(|(id, slug)| {
            let repo_commits = repo_commit_counts.get(id).copied().unwrap_or(0);
            let mut contributors: Vec<OwnershipShare> = per_repo_people
                .get(id)
                .map(|people| {
                    people
                        .iter()
                        .map(|(email, a)| OwnershipShare {
                            email: email.clone(),
                            name: a.name.clone(),
                            commits: a.commits,
                            share: ratio(a.commits, repo_commits),
                            is_agent: a.is_agent,
                        })
                        .collect()
                })
                .unwrap_or_default();
            contributors.sort_by(|a, b| {
                b.share
                    .partial_cmp(&a.share)
                    .unwrap_or(std::cmp::Ordering::Equal)
                    .then_with(|| a.email.cmp(&b.email))
            });
            RepoInvolvement {
                repo_id: id.clone(),
                slug: slug.clone(),
                commits: repo_commits,
                contributors,
            }
        })
        .collect();
    repos_out.sort_by(|a, b| {
        b.commits
            .cmp(&a.commits)
            .then_with(|| a.repo_id.cmp(&b.repo_id))
    });

    let mut people_out: Vec<PersonInvolvement> = person_totals
        .into_iter()
        .map(|(email, pt)| {
            let mut repos_v: Vec<PersonRepoShare> = pt
                .per_repo
                .iter()
                .filter_map(|(rid, cnt)| {
                    slug_of.get(rid.as_str()).map(|slug| PersonRepoShare {
                        repo_id: rid.clone(),
                        slug: slug.to_string(),
                        commits: *cnt,
                        share: ratio(*cnt, pt.total),
                    })
                })
                .collect();
            repos_v.sort_by(|a, b| {
                b.commits
                    .cmp(&a.commits)
                    .then_with(|| a.repo_id.cmp(&b.repo_id))
            });
            PersonInvolvement {
                email,
                name: pt.name,
                is_agent: pt.is_agent,
                total_commits: pt.total,
                repo_count: repos_v.len() as u32,
                repos: repos_v,
            }
        })
        .collect();
    people_out.sort_by(|a, b| {
        b.total_commits
            .cmp(&a.total_commits)
            .then_with(|| a.email.cmp(&b.email))
    });

    Involvement {
        repos: repos_out,
        people: people_out,
    }
}

/// Compose the range-scoped engineering-health payload.
///
/// The repo/person ownership breakdown is deliberately NOT part of
/// [`EngHealth`] — call [`involvement`] for that; it is a different question
/// with a different natural scope.
pub fn compute(
    commits: &[Commit],
    items: &[WorkItem],
    known: &[Contributor],
    from: &str,
    to: &str,
) -> EngHealth {
    EngHealth {
        range: analytics::Range {
            from: from.to_string(),
            to: to.to_string(),
            days: range_days(from, to),
        },
        dora: dora(items, commits, from, to),
        bus_factor: bus_factor(commits, known, from, to),
        review: review_health(items),
        quality: quality(commits, items, from, to),
    }
}

/// One contributor's six dimensions merged across every repo they touched.
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct MergedContribution {
    pub dimensions: Dimensions,
    pub raw: DimensionRaw,
    pub agent_pct: f64,
    pub composite: f64,
}

/// Merge a contributor's per-repo [`Contribution`] rows into one row.
///
/// Per-repo rows are min-max normalized **within their own repo**, so a plain
/// mean would let a single commit to a tiny repo count as much as sustained
/// work on the main one. Each dimension is therefore a **commit-weighted**
/// mean, weighting a row by its `human_commits + agent_commits` (falling back
/// to 1.0 when that is zero, so a pure reviewer still counts).
///
/// `raw` fields are absolute counts rather than normalized scores, so those
/// are summed. `composite` is recomputed from the merged dimensions against
/// the caller's current `weights` — averaging the stored per-row composites
/// would bake in whatever weights were in force when each row was derived.
pub fn merge_contributions(rows: &[Contribution], weights: &Weights) -> MergedContribution {
    let mut raw = DimensionRaw::default();
    let mut acc = Dimensions {
        shipped: 0.0,
        review: 0.0,
        effort: 0.0,
        quality: 0.0,
        ownership: 0.0,
        durability: 0.0,
    };
    let mut total_weight = 0.0_f64;

    for r in rows {
        let commits = r.raw.human_commits + r.raw.agent_commits;
        let w = if commits == 0 { 1.0 } else { commits as f64 };
        total_weight += w;

        acc.shipped += r.dimensions.shipped * w;
        acc.review += r.dimensions.review * w;
        acc.effort += r.dimensions.effort * w;
        acc.quality += r.dimensions.quality * w;
        acc.ownership += r.dimensions.ownership * w;
        acc.durability += r.dimensions.durability * w;

        raw.merged_prs += r.raw.merged_prs;
        raw.closed_issues += r.raw.closed_issues;
        raw.reviews_done += r.raw.reviews_done;
        raw.effort_points += r.raw.effort_points;
        raw.reverts_caused += r.raw.reverts_caused;
        raw.bug_intros += r.raw.bug_intros;
        raw.areas_owned += r.raw.areas_owned;
        raw.surviving_lines += r.raw.surviving_lines;
        raw.authored_lines += r.raw.authored_lines;
        raw.human_commits += r.raw.human_commits;
        raw.agent_commits += r.raw.agent_commits;
    }

    let dimensions = if total_weight > 0.0 {
        Dimensions {
            shipped: acc.shipped / total_weight,
            review: acc.review / total_weight,
            effort: acc.effort / total_weight,
            quality: acc.quality / total_weight,
            ownership: acc.ownership / total_weight,
            durability: acc.durability / total_weight,
        }
    } else {
        acc // all zeros — empty input must stay finite, never NaN
    };

    let all_commits = raw.human_commits + raw.agent_commits;
    MergedContribution {
        dimensions,
        raw,
        agent_pct: if all_commits == 0 {
            0.0
        } else {
            raw.agent_commits as f64 / all_commits as f64
        },
        composite: crate::derive::composite(&dimensions, weights),
    }
}

fn ratio(num: u32, den: u32) -> f64 {
    if den == 0 {
        0.0
    } else {
        num as f64 / den as f64
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ids::{ContributorId, RepoId, WorkItemId};

    /// `(additions, deletions, is_merge)` grouped so the fixture stays under
    /// clippy's positional-argument ceiling and reads less ambiguously.
    type Shape = (u32, u32, bool);

    fn commit_full(
        sha: &str,
        at: &str,
        email: &str,
        (add, del, is_merge): Shape,
        summary: &str,
        repo: &str,
    ) -> Commit {
        Commit {
            sha: sha.into(),
            repo_id: RepoId::from(repo),
            author_email: email.into(),
            author_name: email.split('@').next().unwrap_or("dev").into(),
            committed_at: at.into(),
            additions: add,
            deletions: del,
            files_changed: 3,
            is_merge,
            is_test_touch: false,
            summary: summary.into(),
        }
    }

    fn commit(sha: &str, at: &str, email: &str, add: u32, del: u32) -> Commit {
        commit_full(sha, at, email, (add, del, false), "work", "r1")
    }

    fn commit_in(repo: &str, sha: &str, at: &str, email: &str, add: u32, del: u32) -> Commit {
        commit_full(sha, at, email, (add, del, false), "work", repo)
    }

    fn pr(
        reference: &str,
        created: &str,
        merged: Option<&str>,
        title: &str,
        labels: &[&str],
    ) -> WorkItem {
        WorkItem {
            id: WorkItemId::from(reference.to_string()),
            repo_id: RepoId::from("r1"),
            kind: WorkKind::Pr,
            external_ref: reference.into(),
            title: title.into(),
            body: String::new(),
            state: if merged.is_some() {
                WorkState::Merged
            } else {
                WorkState::Open
            },
            author_login: Some("dev".into()),
            labels: labels.iter().map(|l| l.to_string()).collect(),
            created_at: created.into(),
            updated_at: created.into(),
            merged_at: merged.map(String::from),
            closed_at: merged.map(String::from),
            files_touched: vec!["src/lib.rs".into()],
        }
    }

    /// Mirrors `gitstate-forge::list_reviews`, which keys a review as
    /// `#<pr>-review-<n>` so several reviews on one PR stay distinct rows.
    fn review(id: &str, target_external_ref: &str, at: &str) -> WorkItem {
        WorkItem {
            id: WorkItemId::from(id.to_string()),
            repo_id: RepoId::from("r1"),
            kind: WorkKind::Review,
            external_ref: format!("{target_external_ref}-review-{id}"),
            title: format!("review of {target_external_ref}"),
            body: String::new(),
            state: WorkState::Done,
            author_login: Some("reviewer".into()),
            labels: vec![],
            created_at: at.into(),
            updated_at: at.into(),
            merged_at: None,
            closed_at: Some(at.into()),
            files_touched: vec![],
        }
    }

    fn contributor(
        primary_email: &str,
        name: &str,
        emails: &[&str],
        is_agent: bool,
    ) -> Contributor {
        Contributor {
            id: ContributorId::from(primary_email.to_string()),
            display_name: name.into(),
            primary_email: primary_email.into(),
            emails: emails.iter().map(|e| e.to_string()).collect(),
            login: None,
            is_agent,
            agent_kind: None,
        }
    }

    // ── dora ──

    #[test]
    fn dora_on_empty_input_is_finite_zeros_and_nones() {
        let d = dora(&[], &[], "2026-06-01", "2026-06-07");
        assert_eq!(d.cycle_p50_hours, None);
        assert_eq!(d.cycle_p90_hours, None);
        assert_eq!(d.change_failure_rate, None);
        assert_eq!(d.merge_frequency_per_week, 0.0);
        assert_eq!(d.deploy_proxy_per_week, 0.0);
        assert_eq!(d.lead_time_samples, 0);
    }

    #[test]
    fn dora_cycle_percentiles_match_cycle_times() {
        let items = vec![pr(
            "#1",
            "2026-06-01T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
            "add widget",
            &[],
        )];
        let d = dora(&items, &[], "2026-06-01", "2026-06-30");
        assert_eq!(d.lead_time_samples, 1);
        assert!((d.cycle_p50_hours.unwrap() - 24.0).abs() < 1e-9);
        assert!((d.cycle_p90_hours.unwrap() - 24.0).abs() < 1e-9);
    }

    #[test]
    fn dora_change_failure_rate_flags_revert_and_hotfix_and_rollback() {
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "Revert \"oops\"",
                &[],
            ),
            pr(
                "#2",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "Hotfix login",
                &[],
            ),
            pr(
                "#3",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "clean feature",
                &["rollback"],
            ),
            pr(
                "#4",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "add widget",
                &[],
            ),
        ];
        let d = dora(&items, &[], "2026-06-01", "2026-06-30");
        assert_eq!(d.change_failure_rate, Some(0.75));
    }

    #[test]
    fn dora_change_failure_rate_is_none_without_merged_prs() {
        let items = vec![pr("#1", "2026-06-01T00:00:00Z", None, "add widget", &[])];
        let d = dora(&items, &[], "2026-06-01", "2026-06-30");
        assert_eq!(d.change_failure_rate, None);
    }

    #[test]
    fn dora_merge_frequency_is_merged_prs_per_week_in_range() {
        // Exactly two weeks (14 days): two merged PRs -> 1.0/week.
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-01T00:00:00Z"),
                "a",
                &[],
            ),
            pr(
                "#2",
                "2026-06-08T00:00:00Z",
                Some("2026-06-08T00:00:00Z"),
                "b",
                &[],
            ),
        ];
        let d = dora(&items, &[], "2026-06-01", "2026-06-14");
        assert!((d.merge_frequency_per_week - 1.0).abs() < 1e-9);
    }

    #[test]
    fn dora_deploy_proxy_counts_merge_commits_per_week_and_documents_it_as_a_proxy() {
        let commits = vec![
            commit_full(
                "a",
                "2026-06-01T00:00:00Z",
                "a@x",
                (1, 0, true),
                "Merge PR #1",
                "r1",
            ),
            commit_full(
                "b",
                "2026-06-02T00:00:00Z",
                "a@x",
                (1, 0, false),
                "work",
                "r1",
            ),
            commit_full(
                "c",
                "2026-06-08T00:00:00Z",
                "a@x",
                (1, 0, true),
                "Merge PR #2",
                "r1",
            ),
        ];
        let d = dora(&[], &commits, "2026-06-01", "2026-06-14");
        assert!(
            (d.deploy_proxy_per_week - 1.0).abs() < 1e-9,
            "2 merge commits over 2 weeks"
        );
    }

    #[test]
    fn dora_ignores_prs_and_commits_outside_range() {
        let items = vec![pr(
            "#1",
            "2020-01-01T00:00:00Z",
            Some("2020-01-02T00:00:00Z"),
            "old",
            &[],
        )];
        let commits = vec![commit_full(
            "a",
            "2020-01-01T00:00:00Z",
            "a@x",
            (1, 0, true),
            "Merge",
            "r1",
        )];
        let d = dora(&items, &commits, "2026-06-01", "2026-06-07");
        assert_eq!(d.lead_time_samples, 0);
        assert_eq!(d.change_failure_rate, None);
        assert_eq!(d.deploy_proxy_per_week, 0.0);
    }

    #[test]
    fn dora_on_inverted_range_never_divides_by_zero() {
        let items = vec![pr(
            "#1",
            "2026-06-01T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
            "a",
            &[],
        )];
        let d = dora(&items, &[], "2026-06-30", "2026-06-01");
        assert_eq!(d.merge_frequency_per_week, 0.0);
        assert_eq!(d.deploy_proxy_per_week, 0.0);
        assert!(d.merge_frequency_per_week.is_finite());
    }

    // ── bus_factor ──

    #[test]
    fn bus_factor_is_zero_on_empty_commits() {
        let bf = bus_factor(&[], &[], "2026-06-01", "2026-06-30");
        assert_eq!(bf.count, 0);
        assert_eq!(bf.top_share, 0.0);
        assert!(bf.contributors.is_empty());
    }

    #[test]
    fn bus_factor_is_one_when_a_single_author_dominates() {
        let commits = vec![
            commit("a", "2026-06-01T00:00:00Z", "a@x", 1, 0),
            commit("b", "2026-06-02T00:00:00Z", "a@x", 1, 0),
            commit("c", "2026-06-03T00:00:00Z", "a@x", 1, 0),
            commit("d", "2026-06-04T00:00:00Z", "b@x", 1, 0),
        ];
        let bf = bus_factor(&commits, &[], "2026-06-01", "2026-06-30");
        assert_eq!(bf.count, 1);
        assert!((bf.top_share - 0.75).abs() < 1e-9);
    }

    #[test]
    fn bus_factor_is_a_majority_not_a_headcount_when_split_evenly() {
        let commits = vec![
            commit("a", "2026-06-01T00:00:00Z", "a@x", 1, 0),
            commit("b", "2026-06-02T00:00:00Z", "b@x", 1, 0),
            commit("c", "2026-06-03T00:00:00Z", "c@x", 1, 0),
        ];
        let bf = bus_factor(&commits, &[], "2026-06-01", "2026-06-30");
        // One third is under half, but two thirds clears it — the bus factor
        // is how many you'd need to LOSE to take out a majority, so 2.
        assert_eq!(bf.count, 2);
    }

    #[test]
    fn bus_factor_reaching_exactly_half_still_counts_as_reached() {
        let commits = vec![
            commit("a", "2026-06-01T00:00:00Z", "a@x", 1, 0),
            commit("b", "2026-06-02T00:00:00Z", "b@x", 1, 0),
        ];
        let bf = bus_factor(&commits, &[], "2026-06-01", "2026-06-30");
        // 50/50 split: the first contributor alone already reaches 50%.
        assert_eq!(bf.count, 1);
    }

    #[test]
    fn bus_factor_folds_merged_identity_aliases() {
        let known = vec![contributor(
            "ada@work",
            "Ada",
            &["ada@work", "ada@home"],
            false,
        )];
        let commits = vec![
            commit("a", "2026-06-01T00:00:00Z", "ada@work", 1, 0),
            commit("b", "2026-06-02T00:00:00Z", "ada@home", 1, 0),
            commit("c", "2026-06-03T00:00:00Z", "b@x", 1, 0),
        ];
        let bf = bus_factor(&commits, &known, "2026-06-01", "2026-06-30");
        assert_eq!(bf.contributors.len(), 2, "aliases collapse into one row");
        let ada = bf
            .contributors
            .iter()
            .find(|c| c.email == "ada@work")
            .unwrap();
        assert_eq!(ada.commits, 2);
        assert_eq!(ada.name, "Ada");
    }

    #[test]
    fn bus_factor_orders_by_share_desc_then_email_asc_on_a_tie() {
        let commits = vec![
            commit("a", "2026-06-01T00:00:00Z", "zeta@x", 1, 0),
            commit("b", "2026-06-02T00:00:00Z", "alpha@x", 1, 0),
        ];
        let bf = bus_factor(&commits, &[], "2026-06-01", "2026-06-30");
        assert_eq!(
            bf.contributors[0].email, "alpha@x",
            "tie broken by email asc"
        );
        assert_eq!(bf.contributors[1].email, "zeta@x");
    }

    #[test]
    fn bus_factor_filters_out_of_range_and_malformed_timestamps() {
        let commits = vec![
            commit("a", "2026-05-01T00:00:00Z", "a@x", 1, 0), // before range
            commit("b", "not-a-timestamp", "a@x", 1, 0),      // garbage
            commit("c", "2026-06-15T00:00:00Z", "a@x", 1, 0), // in range
        ];
        let bf = bus_factor(&commits, &[], "2026-06-01", "2026-06-30");
        assert_eq!(bf.contributors.len(), 1);
        assert_eq!(bf.contributors[0].commits, 1);
    }

    // ── review_health ──

    #[test]
    fn review_health_on_empty_input_is_finite_zero() {
        let r = review_health(&[]);
        assert_eq!(r.merged_prs, 0);
        assert_eq!(r.reviewed_pr_share, 0.0);
        assert_eq!(r.unreviewed_merged, 0);
    }

    #[test]
    fn review_health_matches_reviews_to_prs_by_external_ref() {
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "a",
                &[],
            ),
            pr(
                "#2",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "b",
                &[],
            ),
            pr("#3", "2026-06-01T00:00:00Z", None, "c", &[]), // not merged
            review("rev1", "#1", "2026-06-01T12:00:00Z"),
        ];
        let r = review_health(&items);
        assert_eq!(r.merged_prs, 2);
        assert_eq!(r.reviews_done, 1);
        assert_eq!(r.unreviewed_merged, 1);
        assert!((r.reviewed_pr_share - 0.5).abs() < 1e-9);
    }

    #[test]
    fn review_health_share_is_zero_not_nan_without_merged_prs() {
        let items = vec![review("rev1", "#1", "2026-06-01T00:00:00Z")];
        let r = review_health(&items);
        assert_eq!(r.reviewed_pr_share, 0.0);
        assert!(r.reviewed_pr_share.is_finite());
    }

    // ── quality ──

    #[test]
    fn quality_on_empty_input_is_finite_zero() {
        let q = quality(&[], &[], "2026-06-01", "2026-06-30");
        assert_eq!(q.test_touch_rate, 0.0);
        assert_eq!(q.avg_commit_size_lines, 0.0);
        assert_eq!(q.large_commit_share, 0.0);
        assert_eq!(q.revert_commits, 0);
    }

    #[test]
    fn quality_computes_test_touch_rate_and_average_size() {
        let mut c1 = commit("a", "2026-06-01T00:00:00Z", "a@x", 10, 0);
        c1.is_test_touch = true;
        let c2 = commit("b", "2026-06-02T00:00:00Z", "a@x", 20, 10);
        let q = quality(&[c1, c2], &[], "2026-06-01", "2026-06-30");
        assert!((q.test_touch_rate - 0.5).abs() < 1e-9);
        assert!((q.avg_commit_size_lines - 20.0).abs() < 1e-9); // (10+30)/2
    }

    #[test]
    fn quality_flags_large_commits_over_the_threshold() {
        let big = commit("a", "2026-06-01T00:00:00Z", "a@x", 300, 200); // 500 lines
        let small = commit("b", "2026-06-02T00:00:00Z", "a@x", 10, 5);
        let q = quality(&[big, small], &[], "2026-06-01", "2026-06-30");
        assert!((q.large_commit_share - 0.5).abs() < 1e-9);
    }

    #[test]
    fn quality_counts_revert_commits_case_insensitively() {
        let commits = vec![
            commit_full(
                "a",
                "2026-06-01T00:00:00Z",
                "a@x",
                (1, 0, false),
                "REVERT \"bad change\"",
                "r1",
            ),
            commit_full(
                "b",
                "2026-06-02T00:00:00Z",
                "a@x",
                (1, 0, false),
                "revert prior commit",
                "r1",
            ),
            commit_full(
                "c",
                "2026-06-03T00:00:00Z",
                "a@x",
                (1, 0, false),
                "add feature",
                "r1",
            ),
        ];
        let q = quality(&commits, &[], "2026-06-01", "2026-06-30");
        assert_eq!(q.revert_commits, 2);
    }

    #[test]
    fn quality_filters_commits_outside_the_range() {
        let commits = vec![
            commit("a", "2026-05-01T00:00:00Z", "a@x", 10, 0),
            commit("b", "2026-06-15T00:00:00Z", "a@x", 20, 0),
        ];
        let q = quality(&commits, &[], "2026-06-01", "2026-06-30");
        assert!((q.avg_commit_size_lines - 20.0).abs() < 1e-9);
    }

    // ── involvement ──

    #[test]
    fn involvement_on_empty_input_is_empty_not_panicking() {
        let inv = involvement(&[], &[], &[], "2026-06-01", "2026-06-30");
        assert!(inv.repos.is_empty());
        assert!(inv.people.is_empty());
    }

    #[test]
    fn involvement_joins_repos_and_people_in_both_directions() {
        let repos = vec![
            ("r1".to_string(), "org/one".to_string()),
            ("r2".to_string(), "org/two".to_string()),
        ];
        let commits = vec![
            commit_in("r1", "a", "2026-06-01T00:00:00Z", "alice@x", 1, 0),
            commit_in("r1", "b", "2026-06-02T00:00:00Z", "alice@x", 1, 0),
            commit_in("r2", "c", "2026-06-03T00:00:00Z", "alice@x", 1, 0),
            commit_in("r1", "d", "2026-06-04T00:00:00Z", "bob@x", 1, 0),
        ];
        let inv = involvement(&commits, &repos, &[], "2026-06-01", "2026-06-30");

        assert_eq!(inv.repos.len(), 2);
        let r1 = inv.repos.iter().find(|r| r.repo_id == "r1").unwrap();
        assert_eq!(r1.commits, 3);
        assert_eq!(r1.contributors.len(), 2);
        let alice_in_r1 = r1
            .contributors
            .iter()
            .find(|c| c.email == "alice@x")
            .unwrap();
        assert!((alice_in_r1.share - (2.0 / 3.0)).abs() < 1e-9);

        assert_eq!(inv.people.len(), 2);
        let alice = inv.people.iter().find(|p| p.email == "alice@x").unwrap();
        assert_eq!(alice.total_commits, 3);
        assert_eq!(alice.repo_count, 2);
        let alice_r1 = alice.repos.iter().find(|r| r.repo_id == "r1").unwrap();
        assert!(
            (alice_r1.share - (2.0 / 3.0)).abs() < 1e-9,
            "share of alice's own total"
        );
    }

    #[test]
    fn involvement_ignores_repos_outside_the_provided_scope_for_the_repo_side() {
        let repos = vec![("r1".to_string(), "org/one".to_string())];
        let commits = vec![
            commit_in("r1", "a", "2026-06-01T00:00:00Z", "alice@x", 1, 0),
            commit_in("r9", "b", "2026-06-02T00:00:00Z", "alice@x", 1, 0), // unscoped repo
        ];
        let inv = involvement(&commits, &repos, &[], "2026-06-01", "2026-06-30");
        assert_eq!(inv.repos.len(), 1, "only the scoped repo is emitted");
        let alice = inv.people.iter().find(|p| p.email == "alice@x").unwrap();
        assert_eq!(
            alice.total_commits, 2,
            "total still counts the unscoped repo's commit"
        );
        assert_eq!(
            alice.repo_count, 1,
            "but it contributes no repo breakdown row"
        );
    }

    #[test]
    fn involvement_orders_repos_by_commits_desc_then_id_asc() {
        let repos = vec![
            ("z1".to_string(), "org/z".to_string()),
            ("a1".to_string(), "org/a".to_string()),
        ];
        let commits = vec![
            commit_in("z1", "a", "2026-06-01T00:00:00Z", "x@x", 1, 0),
            commit_in("a1", "b", "2026-06-02T00:00:00Z", "x@x", 1, 0),
        ];
        let inv = involvement(&commits, &repos, &[], "2026-06-01", "2026-06-30");
        // Tied commit counts (1 each) -> ordered by repo_id asc.
        assert_eq!(inv.repos[0].repo_id, "a1");
        assert_eq!(inv.repos[1].repo_id, "z1");
    }

    // ── compute ──

    #[test]
    fn compute_on_empty_input_never_produces_nan_or_inf() {
        let h = compute(&[], &[], &[], "2026-06-01", "2026-06-07");
        assert_eq!(h.range.days, 7);
        assert_eq!(h.dora.merge_frequency_per_week, 0.0);
        assert_eq!(h.dora.deploy_proxy_per_week, 0.0);
        assert_eq!(h.dora.change_failure_rate, None);
        assert_eq!(h.bus_factor.count, 0);
        assert_eq!(h.review.reviewed_pr_share, 0.0);
        assert_eq!(h.quality.test_touch_rate, 0.0);

        // Exhaustively check every f64-bearing field is finite.
        assert!(h.dora.merge_frequency_per_week.is_finite());
        assert!(h.dora.deploy_proxy_per_week.is_finite());
        assert!(h.bus_factor.top_share.is_finite());
        assert!(h.review.reviewed_pr_share.is_finite());
        assert!(h.quality.test_touch_rate.is_finite());
        assert!(h.quality.avg_commit_size_lines.is_finite());
        assert!(h.quality.large_commit_share.is_finite());
    }

    #[test]
    fn compute_composes_every_sub_metric_consistently() {
        let known = vec![contributor("a@x", "A", &["a@x"], false)];
        let commits = vec![
            commit_full(
                "a",
                "2026-06-01T00:00:00Z",
                "a@x",
                (500, 0, true),
                "Merge PR #1",
                "r1",
            ),
            commit("b", "2026-06-02T00:00:00Z", "b@x", 10, 0),
        ];
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "add widget",
                &[],
            ),
            review("rev1", "#1", "2026-06-02T01:00:00Z"),
        ];
        let h = compute(&commits, &items, &known, "2026-06-01", "2026-06-07");

        assert_eq!(h.range.from, "2026-06-01");
        assert_eq!(h.range.to, "2026-06-07");
        assert_eq!(h.dora.lead_time_samples, 1);
        assert_eq!(h.review.merged_prs, 1);
        assert_eq!(h.review.unreviewed_merged, 0);
        assert_eq!(h.bus_factor.contributors.len(), 2);
        assert!((h.quality.large_commit_share - 0.5).abs() < 1e-9);
    }

    // ── shared helpers ──

    #[test]
    fn range_days_counts_inclusively_and_zero_on_inverted_range() {
        assert_eq!(range_days("2026-06-01", "2026-06-07"), 7);
        assert_eq!(range_days("2026-06-07", "2026-06-01"), 0);
        assert_eq!(range_days("garbage", "2026-06-01"), 0);
    }

    // ── review ref parsing (the forge's `#<pr>-review-<n>` convention) ──

    #[test]
    fn pr_ref_of_review_takes_the_prefix_before_the_marker() {
        assert_eq!(pr_ref_of_review("#42-review-1"), Some("#42"));
        assert_eq!(pr_ref_of_review("#42-review-17"), Some("#42"));
        // A PR's own ref is not a review ref.
        assert_eq!(pr_ref_of_review("#42"), None);
        assert_eq!(pr_ref_of_review(""), None);
        // A leading marker would yield an empty key — reject it rather than
        // matching every PR whose ref is "".
        assert_eq!(pr_ref_of_review("-review-1"), None);
    }

    #[test]
    fn review_share_cannot_exceed_one_when_reviews_point_at_unknown_prs() {
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "a",
                &[],
            ),
            // Three reviews, two of them against PRs we don't have.
            review("r1", "#1", "2026-06-01T12:00:00Z"),
            review("r2", "#99", "2026-06-01T12:00:00Z"),
            review("r3", "#98", "2026-06-01T12:00:00Z"),
        ];
        let r = review_health(&items);
        assert_eq!(r.reviews_done, 3);
        assert_eq!(r.merged_prs, 1);
        assert!(r.reviewed_pr_share <= 1.0, "got {}", r.reviewed_pr_share);
        assert_eq!(r.unreviewed_merged, 0);
    }

    #[test]
    fn several_reviews_on_one_pr_count_it_once() {
        let items = vec![
            pr(
                "#1",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "a",
                &[],
            ),
            pr(
                "#2",
                "2026-06-01T00:00:00Z",
                Some("2026-06-02T00:00:00Z"),
                "b",
                &[],
            ),
            review("r1", "#1", "2026-06-01T12:00:00Z"),
            review("r2", "#1", "2026-06-01T13:00:00Z"),
        ];
        let r = review_health(&items);
        assert_eq!(r.reviews_done, 2);
        assert!(
            (r.reviewed_pr_share - 0.5).abs() < 1e-9,
            "one of two PRs reviewed"
        );
        assert_eq!(r.unreviewed_merged, 1);
    }

    #[test]
    fn a_review_in_one_repo_does_not_satisfy_the_same_pr_number_in_another() {
        // PR numbers restart per repo, so this is the exact collision a
        // repo-blind match would produce: two repos both have a merged "#7",
        // only one of which was actually reviewed.
        let mut pr_a = pr(
            "#7",
            "2026-06-01T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
            "a",
            &[],
        );
        pr_a.repo_id = RepoId::from("repo-a");
        let mut pr_b = pr(
            "#7",
            "2026-06-01T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
            "b",
            &[],
        );
        pr_b.repo_id = RepoId::from("repo-b");
        let mut rev = review("r1", "#7", "2026-06-01T12:00:00Z");
        rev.repo_id = RepoId::from("repo-a");

        let r = review_health(&[pr_a, pr_b, rev]);
        assert_eq!(r.merged_prs, 2);
        assert_eq!(
            r.unreviewed_merged, 1,
            "repo-b's #7 has no review of its own and must not borrow repo-a's"
        );
        assert!((r.reviewed_pr_share - 0.5).abs() < 1e-9);
    }

    // ── cross-repo contribution merge ──

    fn contribution(dims: f64, human: u32, agent: u32) -> Contribution {
        Contribution {
            contributor_id: crate::ids::ContributorId::from("c1"),
            repo_id: RepoId::from("r1"),
            from: "1970-01-01T00:00:00Z".into(),
            to: "9999-12-31T23:59:59Z".into(),
            dimensions: Dimensions {
                shipped: dims,
                review: dims,
                effort: dims,
                quality: dims,
                ownership: dims,
                durability: dims,
            },
            raw: DimensionRaw {
                merged_prs: 2,
                closed_issues: 1,
                reviews_done: 3,
                effort_points: 5.0,
                human_commits: human,
                agent_commits: agent,
                surviving_lines: 100,
                authored_lines: 200,
                ..DimensionRaw::default()
            },
            agent_pct: 0.0,
            composite: 0.0,
        }
    }

    #[test]
    fn merging_no_contributions_is_finite_zero() {
        let m = merge_contributions(&[], &Weights::default_weights());
        assert_eq!(m.dimensions.shipped, 0.0);
        assert_eq!(m.agent_pct, 0.0);
        assert!(m.composite.is_finite());
        assert_eq!(m.raw.merged_prs, 0);
    }

    #[test]
    fn merging_one_contribution_round_trips_its_dimensions() {
        let m = merge_contributions(&[contribution(80.0, 10, 0)], &Weights::default_weights());
        assert!((m.dimensions.shipped - 80.0).abs() < 1e-9);
        assert!((m.dimensions.durability - 80.0).abs() < 1e-9);
    }

    #[test]
    fn merging_weights_dimensions_by_commit_volume() {
        // Per-repo rows are normalized WITHIN their repo, so a plain mean
        // would let 1 commit to a tiny repo outweigh 99 to the main one.
        let rows = vec![contribution(0.0, 99, 0), contribution(100.0, 1, 0)];
        let m = merge_contributions(&rows, &Weights::default_weights());
        let plain_mean = 50.0;
        assert!(
            m.dimensions.shipped < plain_mean,
            "commit-weighted mean should sit near the heavier row, got {}",
            m.dimensions.shipped
        );
        assert!(
            (m.dimensions.shipped - 1.0).abs() < 1e-6,
            "got {}",
            m.dimensions.shipped
        );
    }

    #[test]
    fn merging_sums_absolute_raw_counts() {
        let rows = vec![contribution(50.0, 4, 1), contribution(50.0, 6, 3)];
        let m = merge_contributions(&rows, &Weights::default_weights());
        assert_eq!(m.raw.merged_prs, 4);
        assert_eq!(m.raw.human_commits, 10);
        assert_eq!(m.raw.agent_commits, 4);
        assert_eq!(m.raw.surviving_lines, 200);
        assert!((m.raw.effort_points - 10.0).abs() < 1e-9);
        // 4 agent of 14 total.
        assert!((m.agent_pct - 4.0 / 14.0).abs() < 1e-9);
    }

    #[test]
    fn merging_a_reviewer_with_no_commits_still_counts() {
        // Weight falls back to 1.0 rather than 0, or a pure reviewer would
        // vanish from the merged row entirely.
        let m = merge_contributions(&[contribution(70.0, 0, 0)], &Weights::default_weights());
        assert!((m.dimensions.review - 70.0).abs() < 1e-9);
        assert_eq!(m.agent_pct, 0.0, "no commits ⇒ 0.0, never NaN");
        assert!(m.agent_pct.is_finite());
    }

    #[test]
    fn ratio_never_divides_by_zero() {
        assert_eq!(ratio(5, 0), 0.0);
        assert_eq!(ratio(0, 0), 0.0);
        assert!((ratio(1, 4) - 0.25).abs() < 1e-9);
    }
}
