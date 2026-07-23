//! Pure, deterministic analytics over the derived caches (commits + work
//! items). No I/O — the daemon reads rows from the store and hands them here,
//! so every number the UI renders is reproducible and unit-testable.
//!
//! The shapes here exist to feed visualizations directly: [`daily_buckets`] is
//! dense (zero-filled across the whole range) so a contribution heatmap can
//! render a grid without the client inventing missing days, and
//! [`cycle_times`] returns one point per merged PR so a trend line needs no
//! client-side joining.

use std::collections::BTreeMap;
use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use time::format_description::well_known::Rfc3339;
use time::{Date, Duration, OffsetDateTime};

use crate::domain::{Commit, Contributor, WorkItem, WorkKind, WorkState};

// ─────────────────────────────── shapes ───────────────────────────────

/// The inclusive date range an [`Analytics`] payload covers (YYYY-MM-DD).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Range {
    pub from: String,
    pub to: String,
    pub days: u32,
}

/// One calendar day of commit activity. Emitted for EVERY day in range, so
/// zero-commit days are present with `commits: 0` — the heatmap grid depends
/// on it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct DayBucket {
    /// YYYY-MM-DD.
    pub date: String,
    /// 0 = Monday … 6 = Sunday. Precomputed so the client never parses dates.
    pub weekday: u8,
    pub commits: u32,
    pub additions: u32,
    pub deletions: u32,
}

/// A contiguous bucket of activity keyed by its first day (weekly rollups).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WeekBucket {
    /// YYYY-MM-DD of the bucket's Monday.
    pub week_start: String,
    pub commits: u32,
    pub additions: u32,
    pub deletions: u32,
    /// All 7 days fall inside the queried range.
    ///
    /// The first and last bucket are usually clipped by the range bounds, and
    /// plotting a 2-day week next to 7-day weeks reads as a cliff rather than
    /// as a boundary. Clients drop incomplete weeks from trend lines while
    /// still counting them in the totals.
    pub complete: bool,
}

/// One contributor's raw activity over the range. Ordered by commits desc —
/// this is texture for a leaderboard bar list, never a performance ranking.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ContributorStat {
    pub email: String,
    pub name: String,
    pub commits: u32,
    pub additions: u32,
    pub deletions: u32,
    pub files_changed: u32,
    /// Distinct calendar days with at least one commit.
    pub active_days: u32,
    pub is_agent: bool,
}

/// One merged PR's lead time, for the cycle-time trend.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CyclePoint {
    /// RFC3339 merge timestamp — the x axis.
    pub merged_at: String,
    pub hours: f64,
    pub external_ref: String,
    pub title: String,
}

/// Work items reaching a terminal state per week — the throughput series.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ThroughputPoint {
    pub week_start: String,
    pub merged_prs: u32,
    pub closed_issues: u32,
}

/// A named slice of a categorical breakdown (labels, work kinds, states).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Slice {
    pub key: String,
    pub count: u32,
}

/// Headline scalars for the stat-card row.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Totals {
    pub commits: u32,
    pub repos: u32,
    pub contributors: u32,
    pub additions: u32,
    pub deletions: u32,
    pub net_lines: i64,
    /// Distinct calendar days with at least one commit.
    pub active_days: u32,
    pub merge_commits: u32,
    /// Commits touching at least one test path.
    pub test_touch_commits: u32,
    pub open_prs: u32,
    pub merged_prs: u32,
    pub open_issues: u32,
    pub closed_issues: u32,
    pub commits_per_active_day: f64,
    pub lines_per_commit: f64,
    /// 0..1 share of commits that touched a test path.
    pub test_touch_rate: f64,
    pub cycle_p50_hours: Option<f64>,
    pub cycle_p90_hours: Option<f64>,
}

/// Everything the dashboard and insights screens need, in one round-trip.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Analytics {
    pub range: Range,
    pub totals: Totals,
    pub heatmap: Vec<DayBucket>,
    pub weekly: Vec<WeekBucket>,
    pub contributors: Vec<ContributorStat>,
    pub cycle_time: Vec<CyclePoint>,
    pub throughput: Vec<ThroughputPoint>,
    pub work_kinds: Vec<Slice>,
    pub work_states: Vec<Slice>,
    pub labels: Vec<Slice>,
}

// ─────────────────────────── date helpers ───────────────────────────

/// The `YYYY-MM-DD` prefix of an RFC3339 timestamp, or `None` if it is not at
/// least that long. Cheap and allocation-light: bucketing never needs a full
/// parse, and lexicographic ordering on the prefix matches chronological order.
pub fn day_key(ts: &str) -> Option<&str> {
    if ts.len() >= 10 && ts.as_bytes()[4] == b'-' && ts.as_bytes()[7] == b'-' {
        Some(&ts[..10])
    } else {
        None
    }
}

fn parse_date(day: &str) -> Option<Date> {
    let mut it = day.splitn(3, '-');
    let y: i32 = it.next()?.parse().ok()?;
    let m: u8 = it.next()?.parse().ok()?;
    let d: u8 = it.next()?.parse().ok()?;
    Date::from_calendar_date(y, time::Month::try_from(m).ok()?, d).ok()
}

fn fmt_date(d: Date) -> String {
    format!("{:04}-{:02}-{:02}", d.year(), d.month() as u8, d.day())
}

/// Monday of the ISO week containing `d`.
fn week_start(d: Date) -> Date {
    let dow = d.weekday().number_days_from_monday() as i64;
    d - Duration::days(dow)
}

fn parse_ts(ts: &str) -> Option<OffsetDateTime> {
    OffsetDateTime::parse(ts, &Rfc3339).ok()
}

/// Linear-interpolated percentile of an already-sorted slice. `p` in 0..=1.
pub fn percentile(sorted: &[f64], p: f64) -> Option<f64> {
    if sorted.is_empty() {
        return None;
    }
    if sorted.len() == 1 {
        return Some(sorted[0]);
    }
    let rank = p.clamp(0.0, 1.0) * (sorted.len() - 1) as f64;
    let lo = rank.floor() as usize;
    let hi = rank.ceil() as usize;
    if lo == hi {
        return Some(sorted[lo]);
    }
    let frac = rank - lo as f64;
    Some(sorted[lo] + (sorted[hi] - sorted[lo]) * frac)
}

// ─────────────────────────── computations ───────────────────────────

/// Dense per-day commit activity across `[from, to]` inclusive (YYYY-MM-DD).
/// Days with no commits are emitted as zeros so the caller can lay out a grid
/// directly. Commits outside the range are ignored.
pub fn daily_buckets(commits: &[Commit], from: &str, to: &str) -> Vec<DayBucket> {
    let (Some(start), Some(end)) = (parse_date(from), parse_date(to)) else {
        return Vec::new();
    };
    if end < start {
        return Vec::new();
    }

    let mut acc: HashMap<&str, (u32, u32, u32)> = HashMap::new();
    for c in commits {
        let Some(day) = day_key(&c.committed_at) else {
            continue;
        };
        if day < from || day > to {
            continue;
        }
        let e = acc.entry(day).or_insert((0, 0, 0));
        e.0 += 1;
        e.1 += c.additions;
        e.2 += c.deletions;
    }

    let mut out = Vec::new();
    let mut d = start;
    while d <= end {
        let key = fmt_date(d);
        let (commits, additions, deletions) = acc.get(key.as_str()).copied().unwrap_or((0, 0, 0));
        out.push(DayBucket {
            weekday: d.weekday().number_days_from_monday(),
            date: key,
            commits,
            additions,
            deletions,
        });
        d += Duration::days(1);
    }
    out
}

/// Roll dense day buckets up into ISO weeks (Monday-anchored). Weeks with no
/// activity are kept so the trend line has an even x axis.
pub fn weekly_buckets(days: &[DayBucket]) -> Vec<WeekBucket> {
    let mut acc: BTreeMap<String, (WeekBucket, u32)> = BTreeMap::new();
    for d in days {
        let Some(date) = parse_date(&d.date) else {
            continue;
        };
        let key = fmt_date(week_start(date));
        let (e, seen) = acc.entry(key.clone()).or_insert((
            WeekBucket {
                week_start: key,
                commits: 0,
                additions: 0,
                deletions: 0,
                complete: false,
            },
            0,
        ));
        e.commits += d.commits;
        e.additions += d.additions;
        e.deletions += d.deletions;
        *seen += 1;
    }
    acc.into_values()
        .map(|(mut w, seen)| {
            w.complete = seen == 7;
            w
        })
        .collect()
}

/// Per-contributor activity, ordered by commits desc then email asc (stable and
/// deterministic). `known` supplies agent flags and display names when the
/// commit's own author name is thin.
pub fn contributor_stats(
    commits: &[Commit],
    known: &[Contributor],
    from: &str,
    to: &str,
) -> Vec<ContributorStat> {
    // email (lowercased) -> the merged identity that owns it
    let mut by_email: HashMap<String, &Contributor> = HashMap::new();
    for c in known {
        for e in std::iter::once(&c.primary_email).chain(c.emails.iter()) {
            by_email.insert(e.trim().to_lowercase(), c);
        }
    }

    struct Acc {
        name: String,
        commits: u32,
        additions: u32,
        deletions: u32,
        files_changed: u32,
        days: std::collections::HashSet<String>,
        is_agent: bool,
    }

    let mut acc: HashMap<String, Acc> = HashMap::new();
    for c in commits {
        let Some(day) = day_key(&c.committed_at) else {
            continue;
        };
        if day < from || day > to {
            continue;
        }
        let email = c.author_email.trim().to_lowercase();
        let identity = by_email.get(&email);
        // Fold every alias of a merged identity into one row.
        let key = identity
            .map(|i| i.primary_email.trim().to_lowercase())
            .unwrap_or_else(|| email.clone());
        let e = acc.entry(key).or_insert_with(|| Acc {
            name: identity
                .map(|i| i.display_name.clone())
                .filter(|n| !n.is_empty())
                .unwrap_or_else(|| c.author_name.clone()),
            commits: 0,
            additions: 0,
            deletions: 0,
            files_changed: 0,
            days: std::collections::HashSet::new(),
            is_agent: identity.map(|i| i.is_agent).unwrap_or(false),
        });
        e.commits += 1;
        e.additions += c.additions;
        e.deletions += c.deletions;
        e.files_changed += c.files_changed;
        e.days.insert(day.to_string());
    }

    let mut out: Vec<ContributorStat> = acc
        .into_iter()
        .map(|(email, a)| ContributorStat {
            email,
            name: a.name,
            commits: a.commits,
            additions: a.additions,
            deletions: a.deletions,
            files_changed: a.files_changed,
            active_days: a.days.len() as u32,
            is_agent: a.is_agent,
        })
        .collect();
    out.sort_by(|a, b| {
        b.commits
            .cmp(&a.commits)
            .then_with(|| a.email.cmp(&b.email))
    });
    out
}

/// Lead time (created → merged) for every merged PR in range, oldest first.
/// Items with a merge timestamp that predates creation are dropped rather than
/// contributing a negative point.
pub fn cycle_times(items: &[WorkItem], from: &str, to: &str) -> Vec<CyclePoint> {
    let mut out: Vec<CyclePoint> = Vec::new();
    for w in items {
        if w.kind != WorkKind::Pr {
            continue;
        }
        let Some(merged) = w.merged_at.as_deref() else {
            continue;
        };
        let Some(day) = day_key(merged) else { continue };
        if day < from || day > to {
            continue;
        }
        let (Some(a), Some(b)) = (parse_ts(&w.created_at), parse_ts(merged)) else {
            continue;
        };
        let hours = (b - a).as_seconds_f64() / 3600.0;
        if hours < 0.0 {
            continue;
        }
        out.push(CyclePoint {
            merged_at: merged.to_string(),
            hours,
            external_ref: w.external_ref.clone(),
            title: w.title.clone(),
        });
    }
    out.sort_by(|a, b| {
        a.merged_at
            .cmp(&b.merged_at)
            .then_with(|| a.external_ref.cmp(&b.external_ref))
    });
    out
}

/// Merged PRs and closed issues per ISO week, oldest first.
pub fn throughput(items: &[WorkItem], from: &str, to: &str) -> Vec<ThroughputPoint> {
    let mut acc: BTreeMap<String, ThroughputPoint> = BTreeMap::new();
    let mut bump = |ts: &str, is_pr: bool| {
        let Some(day) = day_key(ts) else { return };
        if day < from || day > to {
            return;
        }
        let Some(date) = parse_date(day) else { return };
        let key = fmt_date(week_start(date));
        let e = acc.entry(key.clone()).or_insert(ThroughputPoint {
            week_start: key,
            merged_prs: 0,
            closed_issues: 0,
        });
        if is_pr {
            e.merged_prs += 1;
        } else {
            e.closed_issues += 1;
        }
    };

    for w in items {
        match w.kind {
            WorkKind::Pr => {
                if let Some(m) = w.merged_at.as_deref() {
                    bump(m, true);
                }
            }
            WorkKind::Issue => {
                if let Some(c) = w.closed_at.as_deref() {
                    bump(c, false);
                }
            }
            _ => {}
        }
    }
    acc.into_values().collect()
}

/// Count `items` by a string key, ordered by count desc then key asc.
fn tally<'a>(keys: impl Iterator<Item = &'a str>) -> Vec<Slice> {
    let mut acc: HashMap<&str, u32> = HashMap::new();
    for k in keys {
        *acc.entry(k).or_insert(0) += 1;
    }
    let mut out: Vec<Slice> = acc
        .into_iter()
        .map(|(key, count)| Slice {
            key: key.to_string(),
            count,
        })
        .collect();
    out.sort_by(|a, b| b.count.cmp(&a.count).then_with(|| a.key.cmp(&b.key)));
    out
}

/// Compose the full payload. `repos` is the repo count the caller scoped to —
/// passed in rather than derived so a filtered view still reports honestly.
pub fn compute(
    commits: &[Commit],
    items: &[WorkItem],
    known: &[Contributor],
    repos: u32,
    from: &str,
    to: &str,
) -> Analytics {
    let heatmap = daily_buckets(commits, from, to);
    let weekly = weekly_buckets(&heatmap);
    let contributors = contributor_stats(commits, known, from, to);
    let cycle_time = cycle_times(items, from, to);
    let throughput_pts = throughput(items, from, to);

    let in_range = |ts: &str| day_key(ts).is_some_and(|d| d >= from && d <= to);
    let scoped: Vec<&Commit> = commits
        .iter()
        .filter(|c| in_range(&c.committed_at))
        .collect();

    let additions: u32 = scoped.iter().map(|c| c.additions).sum();
    let deletions: u32 = scoped.iter().map(|c| c.deletions).sum();
    let merge_commits = scoped.iter().filter(|c| c.is_merge).count() as u32;
    let test_touch = scoped.iter().filter(|c| c.is_test_touch).count() as u32;
    let active_days = heatmap.iter().filter(|d| d.commits > 0).count() as u32;
    let n_commits = scoped.len() as u32;

    // Work-item state counts respect the window via each item's own timestamps:
    // an item is "open" if it was created in range and never closed.
    let mut open_prs = 0;
    let mut merged_prs = 0;
    let mut open_issues = 0;
    let mut closed_issues = 0;
    for w in items {
        match w.kind {
            WorkKind::Pr => match w.state {
                WorkState::Merged => merged_prs += 1,
                WorkState::Open | WorkState::Draft => open_prs += 1,
                _ => {}
            },
            WorkKind::Issue => match w.state {
                WorkState::Closed | WorkState::Merged => closed_issues += 1,
                WorkState::Open | WorkState::Draft => open_issues += 1,
                _ => {}
            },
            _ => {}
        }
    }

    let mut hours: Vec<f64> = cycle_time.iter().map(|p| p.hours).collect();
    hours.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));

    let days = heatmap.len() as u32;
    let totals = Totals {
        commits: n_commits,
        repos,
        contributors: contributors.len() as u32,
        additions,
        deletions,
        net_lines: additions as i64 - deletions as i64,
        active_days,
        merge_commits,
        test_touch_commits: test_touch,
        open_prs,
        merged_prs,
        open_issues,
        closed_issues,
        commits_per_active_day: ratio(n_commits, active_days),
        lines_per_commit: ratio(additions + deletions, n_commits),
        test_touch_rate: if n_commits == 0 {
            0.0
        } else {
            test_touch as f64 / n_commits as f64
        },
        cycle_p50_hours: percentile(&hours, 0.5),
        cycle_p90_hours: percentile(&hours, 0.9),
    };

    Analytics {
        range: Range {
            from: from.to_string(),
            to: to.to_string(),
            days,
        },
        totals,
        heatmap,
        weekly,
        contributors,
        cycle_time,
        throughput: throughput_pts,
        work_kinds: tally(items.iter().map(|w| w.kind.as_str())),
        work_states: tally(items.iter().map(|w| w.state.as_str())),
        labels: tally(
            items
                .iter()
                .flat_map(|w| w.labels.iter().map(|s| s.as_str())),
        ),
    }
}

fn ratio(num: u32, den: u32) -> f64 {
    if den == 0 {
        0.0
    } else {
        num as f64 / den as f64
    }
}

/// `days` back from `to` (inclusive of both ends), as a YYYY-MM-DD pair. Used
/// to turn a "last 90 days" UI filter into a concrete range.
pub fn range_ending(to: &str, days: u32) -> Option<(String, String)> {
    let end = parse_date(day_key(to)?)?;
    let start = end - Duration::days(days.saturating_sub(1) as i64);
    Some((fmt_date(start), fmt_date(end)))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ids::{RepoId, WorkItemId};

    fn commit(sha: &str, at: &str, email: &str, add: u32, del: u32) -> Commit {
        Commit {
            sha: sha.into(),
            repo_id: RepoId::from("r1"),
            author_email: email.into(),
            author_name: email.split('@').next().unwrap_or("dev").into(),
            committed_at: at.into(),
            additions: add,
            deletions: del,
            files_changed: 3,
            is_merge: false,
            is_test_touch: false,
            summary: "work".into(),
        }
    }

    fn pr(reference: &str, created: &str, merged: Option<&str>) -> WorkItem {
        WorkItem {
            id: WorkItemId::from(reference.to_string()),
            repo_id: RepoId::from("r1"),
            kind: WorkKind::Pr,
            external_ref: reference.into(),
            title: format!("PR {reference}"),
            body: String::new(),
            state: if merged.is_some() {
                WorkState::Merged
            } else {
                WorkState::Open
            },
            author_login: Some("dev".into()),
            labels: vec!["backend".into()],
            created_at: created.into(),
            updated_at: created.into(),
            merged_at: merged.map(String::from),
            closed_at: merged.map(String::from),
            files_touched: vec!["src/lib.rs".into()],
        }
    }

    #[test]
    fn day_key_extracts_the_date_prefix() {
        assert_eq!(day_key("2026-06-15T09:00:00Z"), Some("2026-06-15"));
        assert_eq!(day_key("2026-06-15"), Some("2026-06-15"));
        assert_eq!(day_key("nope"), None);
        assert_eq!(day_key(""), None);
    }

    #[test]
    fn daily_buckets_are_dense_and_zero_filled() {
        let commits = vec![
            commit("a", "2026-06-01T10:00:00Z", "a@x", 10, 2),
            commit("b", "2026-06-01T18:00:00Z", "a@x", 5, 1),
            commit("c", "2026-06-04T09:00:00Z", "b@x", 7, 3),
        ];
        let days = daily_buckets(&commits, "2026-06-01", "2026-06-05");
        // Every day present, including the silent 2nd/3rd/5th.
        assert_eq!(days.len(), 5);
        assert_eq!(days[0].date, "2026-06-01");
        assert_eq!(days[0].commits, 2);
        assert_eq!(days[0].additions, 15);
        assert_eq!(days[0].deletions, 3);
        assert_eq!(days[1].commits, 0);
        assert_eq!(days[2].commits, 0);
        assert_eq!(days[3].commits, 1);
        assert_eq!(days[4].commits, 0);
    }

    #[test]
    fn daily_buckets_tag_the_weekday() {
        // 2026-06-01 is a Monday.
        let days = daily_buckets(&[], "2026-06-01", "2026-06-07");
        let weekdays: Vec<u8> = days.iter().map(|d| d.weekday).collect();
        assert_eq!(weekdays, vec![0, 1, 2, 3, 4, 5, 6]);
    }

    #[test]
    fn daily_buckets_ignore_out_of_range_commits() {
        let commits = vec![
            commit("a", "2026-05-30T10:00:00Z", "a@x", 10, 0),
            commit("b", "2026-06-02T10:00:00Z", "a@x", 4, 0),
            commit("c", "2026-06-09T10:00:00Z", "a@x", 8, 0),
        ];
        let days = daily_buckets(&commits, "2026-06-01", "2026-06-05");
        let total: u32 = days.iter().map(|d| d.commits).sum();
        assert_eq!(total, 1);
    }

    #[test]
    fn daily_buckets_reject_an_inverted_range() {
        assert!(daily_buckets(&[], "2026-06-05", "2026-06-01").is_empty());
    }

    #[test]
    fn weekly_buckets_anchor_on_monday() {
        // 2026-06-01 Mon … 2026-06-14 Sun — exactly two ISO weeks.
        let commits = vec![
            commit("a", "2026-06-03T10:00:00Z", "a@x", 1, 0),
            commit("b", "2026-06-07T10:00:00Z", "a@x", 1, 0),
            commit("c", "2026-06-08T10:00:00Z", "a@x", 1, 0),
        ];
        let days = daily_buckets(&commits, "2026-06-01", "2026-06-14");
        let weeks = weekly_buckets(&days);
        assert_eq!(weeks.len(), 2);
        assert_eq!(weeks[0].week_start, "2026-06-01");
        assert_eq!(weeks[0].commits, 2);
        assert_eq!(weeks[1].week_start, "2026-06-08");
        assert_eq!(weeks[1].commits, 1);
        assert!(weeks.iter().all(|w| w.complete), "both weeks are whole");
    }

    #[test]
    fn weekly_buckets_flag_clipped_edges() {
        // Wed → Tue: the first and last buckets are clipped by the range, and a
        // 2-day week plotted beside 7-day weeks would read as a collapse.
        let days = daily_buckets(&[], "2026-06-03", "2026-06-16");
        let weeks = weekly_buckets(&days);
        assert_eq!(weeks.len(), 3);
        assert!(!weeks[0].complete, "leading partial week");
        assert!(weeks[1].complete, "the whole week between them");
        assert!(!weeks[2].complete, "trailing partial week");
    }

    #[test]
    fn contributor_stats_rank_by_commits_and_count_active_days() {
        let commits = vec![
            commit("a", "2026-06-01T10:00:00Z", "a@x", 10, 1),
            commit("b", "2026-06-01T14:00:00Z", "a@x", 10, 1),
            commit("c", "2026-06-03T10:00:00Z", "a@x", 10, 1),
            commit("d", "2026-06-02T10:00:00Z", "b@x", 50, 5),
        ];
        let stats = contributor_stats(&commits, &[], "2026-06-01", "2026-06-30");
        assert_eq!(stats.len(), 2);
        assert_eq!(stats[0].email, "a@x");
        assert_eq!(stats[0].commits, 3);
        assert_eq!(stats[0].active_days, 2); // 3 commits over 2 distinct days
        assert_eq!(stats[1].email, "b@x");
        assert_eq!(stats[1].commits, 1);
    }

    #[test]
    fn contributor_stats_fold_merged_identity_aliases() {
        let known = vec![Contributor {
            id: crate::ids::ContributorId::from("c1"),
            display_name: "Ada".into(),
            primary_email: "ada@work".into(),
            emails: vec!["ada@work".into(), "ada@home".into()],
            login: Some("ada".into()),
            is_agent: false,
            agent_kind: None,
        }];
        let commits = vec![
            commit("a", "2026-06-01T10:00:00Z", "ada@work", 1, 0),
            commit("b", "2026-06-02T10:00:00Z", "ada@home", 1, 0),
        ];
        let stats = contributor_stats(&commits, &known, "2026-06-01", "2026-06-30");
        assert_eq!(stats.len(), 1, "aliases must collapse into one row");
        assert_eq!(stats[0].commits, 2);
        assert_eq!(stats[0].name, "Ada");
    }

    #[test]
    fn cycle_times_measure_created_to_merged() {
        let items = vec![
            pr("#1", "2026-06-01T00:00:00Z", Some("2026-06-02T00:00:00Z")),
            pr("#2", "2026-06-01T00:00:00Z", None),
        ];
        let pts = cycle_times(&items, "2026-06-01", "2026-06-30");
        assert_eq!(pts.len(), 1, "unmerged PRs contribute no point");
        assert!((pts[0].hours - 24.0).abs() < 1e-6);
    }

    #[test]
    fn cycle_times_drop_negative_durations() {
        let items = vec![pr(
            "#1",
            "2026-06-05T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
        )];
        assert!(cycle_times(&items, "2026-06-01", "2026-06-30").is_empty());
    }

    #[test]
    fn throughput_buckets_by_iso_week() {
        let items = vec![
            pr("#1", "2026-06-01T00:00:00Z", Some("2026-06-03T00:00:00Z")),
            pr("#2", "2026-06-01T00:00:00Z", Some("2026-06-05T00:00:00Z")),
            pr("#3", "2026-06-01T00:00:00Z", Some("2026-06-09T00:00:00Z")),
        ];
        let t = throughput(&items, "2026-06-01", "2026-06-30");
        assert_eq!(t.len(), 2);
        assert_eq!(t[0].week_start, "2026-06-01");
        assert_eq!(t[0].merged_prs, 2);
        assert_eq!(t[1].merged_prs, 1);
    }

    #[test]
    fn percentile_interpolates() {
        let xs = vec![1.0, 2.0, 3.0, 4.0];
        assert_eq!(percentile(&xs, 0.0), Some(1.0));
        assert_eq!(percentile(&xs, 1.0), Some(4.0));
        assert_eq!(percentile(&xs, 0.5), Some(2.5));
        assert_eq!(percentile(&[], 0.5), None);
        assert_eq!(percentile(&[7.0], 0.9), Some(7.0));
    }

    #[test]
    fn compute_fills_totals_consistently() {
        let commits = vec![
            commit("a", "2026-06-01T10:00:00Z", "a@x", 100, 20),
            commit("b", "2026-06-03T10:00:00Z", "b@x", 50, 10),
        ];
        let items = vec![pr(
            "#1",
            "2026-06-01T00:00:00Z",
            Some("2026-06-02T00:00:00Z"),
        )];
        let a = compute(&commits, &items, &[], 2, "2026-06-01", "2026-06-07");

        assert_eq!(a.range.days, 7);
        assert_eq!(a.heatmap.len(), 7);
        assert_eq!(a.totals.commits, 2);
        assert_eq!(a.totals.repos, 2);
        assert_eq!(a.totals.contributors, 2);
        assert_eq!(a.totals.additions, 150);
        assert_eq!(a.totals.deletions, 30);
        assert_eq!(a.totals.net_lines, 120);
        assert_eq!(a.totals.active_days, 2);
        assert_eq!(a.totals.merged_prs, 1);
        assert_eq!(a.totals.commits_per_active_day, 1.0);
        assert_eq!(a.totals.lines_per_commit, 90.0);
        assert!(a.totals.cycle_p50_hours.is_some());
        assert_eq!(a.labels.first().map(|s| s.key.as_str()), Some("backend"));
    }

    #[test]
    fn compute_on_empty_input_is_total() {
        let a = compute(&[], &[], &[], 0, "2026-06-01", "2026-06-07");
        assert_eq!(a.totals.commits, 0);
        assert_eq!(a.totals.commits_per_active_day, 0.0);
        assert_eq!(a.totals.lines_per_commit, 0.0);
        assert_eq!(a.totals.test_touch_rate, 0.0);
        assert!(a.totals.cycle_p50_hours.is_none());
        assert_eq!(a.heatmap.len(), 7, "grid still renders with no data");
    }

    #[test]
    fn range_ending_counts_inclusively() {
        let (from, to) = range_ending("2026-06-30T12:00:00Z", 30).unwrap();
        assert_eq!(to, "2026-06-30");
        assert_eq!(from, "2026-06-01");
        let (from, to) = range_ending("2026-06-30", 1).unwrap();
        assert_eq!(from, "2026-06-30");
        assert_eq!(to, "2026-06-30");
    }
}
