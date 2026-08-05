//! Pure, deterministic derivation of `cohort_key` / `size_bucket` /
//! `change_type` from a PR + its diff metadata. Zero I/O.
//!
//! Ported 1:1 from Go's `internal/calibration/cohort.go`. Every function here
//! keeps the original's exact thresholds, tie-break rules, and ordering —
//! see `docs/PORT-PLAN.md` §2 wave 2 for why numerical fidelity (not just
//! "compiles and looks similar") is this domain's whole risk.

/// The size signals used to bucket a change. Mirrors Go's `DiffStats`: taken
/// from whatever diff metadata is available for the change, never requiring
/// a re-read of the raw diff.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DiffStats {
    pub additions: u32,
    pub deletions: u32,
    pub changed_files: u32,
    /// Distinct top-level directories touched (e.g. "internal", "web").
    /// Optional — when empty, area cohorts are skipped.
    pub top_dirs: Vec<String>,
}

/// The minimal PR context needed to derive cohorts. Mirrors Go's `PRMeta`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct PrMeta {
    pub repo_id: String,
    pub title: String,
    pub branch: String,
    pub paths: Vec<String>,
}

// ── size_bucket ──────────────────────────────────────────────────────────

const SIZE_ORDER: [&str; 5] = ["xs", "s", "m", "l", "xl"];

fn bucket_rank(b: &str) -> usize {
    SIZE_ORDER.iter().position(|v| *v == b).unwrap_or(0)
}

/// Maps `v` onto xs|s|m|l|xl. `t1..t4` are the upper bounds of xs, s, m, l
/// respectively (`v > t4` => xl).
fn bucket_by_thresholds(v: u32, t1: u32, t2: u32, t3: u32, t4: u32) -> &'static str {
    if v <= t1 {
        "xs"
    } else if v <= t2 {
        "s"
    } else if v <= t3 {
        "m"
    } else if v <= t4 {
        "l"
    } else {
        "xl"
    }
}

/// Classifies the magnitude of a change into xs|s|m|l|xl using both churn
/// (additions+deletions) and the number of files touched — whichever is
/// larger wins, so a one-file 2000-line generated change and a 40-file
/// refactor both land in xl. Deterministic and monotonic.
pub fn size_bucket(s: &DiffStats) -> &'static str {
    let churn = s.additions + s.deletions;
    let mut files = s.changed_files;
    if (files as usize) < s.top_dirs.len() {
        files = s.top_dirs.len() as u32;
    }

    let by_churn = bucket_by_thresholds(churn, 10, 50, 250, 1000);
    let by_files = bucket_by_thresholds(files, 1, 3, 10, 25);

    // The coarser (larger) of the two classifications wins.
    if bucket_rank(by_files) > bucket_rank(by_churn) {
        by_files
    } else {
        by_churn
    }
}

// ── change_type ──────────────────────────────────────────────────────────

/// Evaluated in order; the first matching family wins. Keeping the order
/// explicit makes the heuristic deterministic and easy to test: fixes beat
/// features (a "fix the feature flag" PR is a fix), docs/test beat code.
const CHANGE_TYPE_KEYWORDS: &[(&str, &[&str])] = &[
    (
        "fix",
        &[
            "fix",
            "bug",
            "hotfix",
            "patch",
            "regression",
            "revert",
            "broken",
        ],
    ),
    (
        "docs",
        &[
            "docs",
            "doc",
            "documentation",
            "readme",
            "changelog",
            "comment",
        ],
    ),
    (
        "test",
        &["test", "tests", "spec", "coverage", "e2e", "fixture"],
    ),
    (
        "refactor",
        &[
            "refactor",
            "cleanup",
            "clean-up",
            "rename",
            "restructure",
            "simplify",
            "dedupe",
        ],
    ),
    (
        "chore",
        &[
            "chore",
            "bump",
            "deps",
            "dependency",
            "dependencies",
            "ci",
            "build",
            "release",
            "lint",
            "format",
            "config",
        ],
    ),
    (
        "feature",
        &[
            "feat",
            "feature",
            "add",
            "implement",
            "introduce",
            "support",
            "new",
        ],
    ),
];

/// Classifies a PR into feature|fix|refactor|chore|docs|test using the
/// title, branch name, and touched paths. Path-only signals (all files under
/// `docs/` or `*_test.go`) are decisive when the title is ambiguous. Default
/// is "feature" — the most common bucket for net-new work. Deterministic.
pub fn change_type(m: &PrMeta) -> &'static str {
    let hay = format!("{} {}", m.title, m.branch).to_lowercase();

    if let Some(t) = conventional_prefix(&hay) {
        return t;
    }

    if only_paths_match(&m.paths, is_doc_path) {
        return "docs";
    }
    if only_paths_match(&m.paths, is_test_path) {
        return "test";
    }

    for (typ, words) in CHANGE_TYPE_KEYWORDS {
        for w in *words {
            if contains_word(&hay, w) {
                return typ;
            }
        }
    }
    "feature"
}

/// Recognises "feat:", "fix(scope):", "feat/foo", "chore-…" style prefixes at
/// the very start of the title/branch.
fn conventional_prefix(hay: &str) -> Option<&'static str> {
    let hay = hay.trim();
    let cut = hay.find([':', '(', '/', '-', ' ']);
    let cut = match cut {
        Some(0) | None => return None,
        Some(c) => c,
    };
    let tok = &hay[..cut];
    match tok {
        "feat" | "feature" => Some("feature"),
        "fix" | "bugfix" | "hotfix" => Some("fix"),
        "docs" | "doc" => Some("docs"),
        "test" | "tests" => Some("test"),
        "refactor" => Some("refactor"),
        "chore" | "build" | "ci" => Some("chore"),
        "perf" => Some("refactor"),
        _ => None,
    }
}

fn is_doc_path(p: &str) -> bool {
    let p = p.to_lowercase();
    p.starts_with("docs/")
        || p.starts_with("doc/")
        || p.ends_with(".md")
        || p.ends_with(".rst")
        || p.contains("readme")
        || p.contains("changelog")
}

fn is_test_path(p: &str) -> bool {
    let p = p.to_lowercase();
    let base = p.rsplit('/').next().unwrap_or(&p);
    p.ends_with("_test.go")
        || p.ends_with(".test.ts")
        || p.ends_with(".test.js")
        || p.ends_with(".spec.ts")
        || p.ends_with(".spec.js")
        || p.starts_with("test/")
        || p.starts_with("tests/")
        || base.starts_with("test_")
        || p.contains("/__tests__/")
}

/// True when `paths` is non-empty and every path satisfies `f`.
fn only_paths_match(paths: &[String], f: impl Fn(&str) -> bool) -> bool {
    if paths.is_empty() {
        return false;
    }
    paths.iter().all(|p| f(p))
}

fn is_alnum(c: char) -> bool {
    c.is_ascii_alphanumeric()
}

/// Reports whether `word` appears in `hay` as a whole token (bounded by
/// non-alphanumeric characters), so "add" does not match "address".
fn contains_word(hay: &str, word: &str) -> bool {
    let bytes = hay.as_bytes();
    let wlen = word.len();
    if wlen == 0 {
        return false;
    }
    let mut idx = 0usize;
    while idx < hay.len() {
        let Some(rel) = hay[idx..].find(word) else {
            return false;
        };
        let start = idx + rel;
        let end = start + wlen;
        let left_ok = start == 0 || !is_alnum(bytes[start - 1] as char);
        let right_ok = end == hay.len() || !is_alnum(bytes[end] as char);
        if left_ok && right_ok {
            return true;
        }
        idx = start + 1;
        if idx >= hay.len() {
            return false;
        }
    }
    false
}

// ── cohort_key ────────────────────────────────────────────────────────────

/// The machine-wide fallback cohort that always exists once there is any
/// merged history; it is the empirical-Bayes prior for sparser cohorts.
pub const GLOBAL_COHORT: &str = "global";

/// Returns the cohort keys for a change ordered RICHEST-FIRST:
///
/// ```text
/// repo:<id>|area:<topdir>   (most specific — same repo, same area)
/// repo:<id>                 (same repo)
/// type:<change_type>        (same kind of change, machine-wide)
/// global                    (machine-wide prior, always last)
/// ```
///
/// The area segment is the top-level directory shared by the change; when
/// the diff touches multiple top dirs the lexicographically-smallest is used
/// so the key is stable. Deterministic.
pub fn cohort_candidates(m: &PrMeta, stats: &DiffStats, change_type: &str) -> Vec<String> {
    let mut out = Vec::new();

    if !m.repo_id.is_empty() {
        if let Some(area) = primary_area(&stats.top_dirs) {
            out.push(format!("repo:{}|area:{area}", m.repo_id));
        }
        out.push(format!("repo:{}", m.repo_id));
    }
    if !change_type.is_empty() {
        out.push(format!("type:{change_type}"));
    }
    out.push(GLOBAL_COHORT.to_string());
    out
}

/// Picks the stable representative top-level directory.
fn primary_area(top_dirs: &[String]) -> Option<String> {
    let mut cleaned: Vec<&str> = top_dirs
        .iter()
        .map(|d| d.trim().trim_matches('/'))
        .filter(|d| !d.is_empty() && *d != ".")
        .collect();
    if cleaned.is_empty() {
        return None;
    }
    cleaned.sort_unstable();
    Some(cleaned[0].to_string())
}

/// Extracts the distinct top-level directories from a set of changed file
/// paths (e.g. "internal/api/x.go" -> "internal"). Files at the repo root
/// contribute the sentinel "root". Deterministic, sorted, de-duplicated.
pub fn top_dirs_from_paths(paths: &[String]) -> Vec<String> {
    let mut seen = std::collections::BTreeSet::new();
    for p in paths {
        let p = p.trim();
        if p.is_empty() {
            continue;
        }
        let p = p.strip_prefix("./").unwrap_or(p);
        let top = match p.find('/') {
            Some(i) => &p[..i],
            None => "root",
        };
        let top = if top.is_empty() { "root" } else { top };
        seen.insert(top.to_string());
    }
    seen.into_iter().collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn stats(additions: u32, deletions: u32, changed_files: u32) -> DiffStats {
        DiffStats {
            additions,
            deletions,
            changed_files,
            top_dirs: vec![],
        }
    }

    #[test]
    fn size_bucket_matches_go_fixtures() {
        // Ported verbatim from cohort_test.go's TestSizeBucket table.
        assert_eq!(size_bucket(&stats(3, 1, 1)), "xs", "tiny one-liner");
        assert_eq!(size_bucket(&stats(30, 10, 2)), "s", "small");
        assert_eq!(size_bucket(&stats(150, 60, 4)), "m", "medium churn");
        assert_eq!(size_bucket(&stats(600, 200, 8)), "l", "large churn");
        assert_eq!(size_bucket(&stats(2000, 500, 3)), "xl", "xl by churn");
        assert_eq!(size_bucket(&stats(40, 10, 30)), "xl", "xl by files");
        assert_eq!(
            size_bucket(&stats(5, 2, 12)),
            "l",
            "l by files beats xs churn"
        );
        let from_topdirs = DiffStats {
            additions: 5,
            deletions: 0,
            changed_files: 0,
            top_dirs: vec!["a".into(), "b".into(), "c".into(), "d".into()],
        };
        assert_eq!(
            size_bucket(&from_topdirs),
            "m",
            "files from topdirs stands in for ChangedFiles"
        );
    }

    fn meta(title: &str, branch: &str, paths: &[&str]) -> PrMeta {
        PrMeta {
            repo_id: String::new(),
            title: title.into(),
            branch: branch.into(),
            paths: paths.iter().map(|s| s.to_string()).collect(),
        }
    }

    #[test]
    fn change_type_matches_go_fixtures() {
        // Ported verbatim from cohort_test.go's TestChangeType table.
        assert_eq!(
            change_type(&meta("feat: add billing webhook", "", &[])),
            "feature"
        );
        assert_eq!(
            change_type(&meta("fix(api): null deref on empty body", "", &[])),
            "fix"
        );
        assert_eq!(change_type(&meta("", "fix/login-redirect", &[])), "fix");
        // "resolve" is not a keyword; default to feature.
        assert_eq!(
            change_type(&meta("Resolve crash when token expires", "", &[])),
            "feature"
        );
        assert_eq!(change_type(&meta("Bug: race in scheduler", "", &[])), "fix");
        assert_eq!(
            change_type(&meta("Update README and docs", "", &[])),
            "docs"
        );
        assert_eq!(
            change_type(&meta("Refactor the sync pipeline", "", &[])),
            "refactor"
        );
        assert_eq!(
            change_type(&meta("chore: bump dependencies", "", &[])),
            "chore"
        );
        assert_eq!(
            change_type(&meta("Add tests for cohort derivation", "", &[])),
            "test"
        );
        assert_eq!(
            change_type(&meta("misc", "", &["docs/intro.md", "README.md"])),
            "docs"
        );
        assert_eq!(
            change_type(&meta(
                "misc",
                "",
                &["internal/x_test.go", "internal/y_test.go"]
            )),
            "test"
        );
        assert_eq!(
            change_type(&meta("Wire the dashboard charts", "", &[])),
            "feature"
        );
        assert_eq!(
            change_type(&meta("Update address validation", "", &[])),
            "feature",
            "add is feature not address"
        );
        assert_eq!(change_type(&meta("implement retry", "", &[])), "feature");
    }

    #[test]
    fn top_dirs_from_paths_matches_go_fixture() {
        let got = top_dirs_from_paths(&[
            "internal/api/x.go".into(),
            "internal/store/y.go".into(),
            "web/app.tsx".into(),
            "README.md".into(),
            "./go.mod".into(),
        ]);
        assert_eq!(got, vec!["internal", "root", "web"]);
    }

    #[test]
    fn cohort_candidates_matches_go_fixtures() {
        // "full richest-first"
        let m = PrMeta {
            repo_id: "R1".into(),
            ..Default::default()
        };
        let stats = DiffStats {
            top_dirs: vec!["web".into(), "internal".into()],
            ..Default::default()
        };
        assert_eq!(
            cohort_candidates(&m, &stats, "feature"),
            vec!["repo:R1|area:internal", "repo:R1", "type:feature", "global"]
        );

        // "no area"
        let stats2 = DiffStats::default();
        assert_eq!(
            cohort_candidates(&m, &stats2, "fix"),
            vec!["repo:R1", "type:fix", "global"]
        );

        // "no repo"
        let m2 = PrMeta::default();
        let stats3 = DiffStats {
            top_dirs: vec!["internal".into()],
            ..Default::default()
        };
        assert_eq!(
            cohort_candidates(&m2, &stats3, "refactor"),
            vec!["type:refactor", "global"]
        );

        // "global only"
        assert_eq!(cohort_candidates(&m2, &stats2, ""), vec!["global"]);
    }

    #[test]
    fn contains_word_respects_boundaries() {
        // "add" must not match inside "address" (regression the Go suite
        // covers via TestChangeType's "add is feature not address" case;
        // exercised here directly against the boundary helper).
        assert!(!contains_word("update address validation", "add"));
        assert!(contains_word("please add this", "add"));
    }
}
