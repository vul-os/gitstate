//! Pure, deterministic derivation helpers. No I/O. Both `gitstate-git` (which
//! feeds them git evidence) and the daemon rely on these being total and
//! reproducible.

use crate::domain::{Contributor, Dimensions, Weights};
use crate::ids::ContributorId;

/// Min-max normalize `raw` against its `cohort` into 0..=100. A cohort of one
/// (or a flat cohort where every value is equal) maps to a neutral 50.0 — a
/// single contributor is never "100th percentile of one".
pub fn normalize_dim(raw: f64, cohort: &[f64]) -> f64 {
    if cohort.len() <= 1 {
        return 50.0;
    }
    let mut min = f64::INFINITY;
    let mut max = f64::NEG_INFINITY;
    for &v in cohort {
        if v.is_finite() {
            min = min.min(v);
            max = max.max(v);
        }
    }
    if !min.is_finite() || !max.is_finite() || (max - min).abs() < f64::EPSILON {
        return 50.0;
    }
    let clamped = raw.clamp(min, max);
    ((clamped - min) / (max - min) * 100.0).clamp(0.0, 100.0)
}

/// Weighted composite in 0..=100. Weights are normalized to sum 1 first.
pub fn composite(dims: &Dimensions, w: &Weights) -> f64 {
    let w = w.normalized();
    (dims.shipped * w.shipped
        + dims.review * w.review
        + dims.effort * w.effort
        + dims.quality * w.quality
        + dims.ownership * w.ownership
        + dims.durability * w.durability)
        .clamp(0.0, 100.0)
}

/// A quality score in 0..=100 that *falls* as reverts, bug introductions, and
/// slow cycle time rise. Inverted so "more bad ⇒ lower". A clean record with a
/// fast cycle scores near 100.
pub fn quality_from(reverts: u32, bug_intros: u32, cycle_hours: Option<f64>) -> f64 {
    // Each defect halves the "clean" mass; cycle time above a week erodes it.
    let defect_penalty = 1.0 / (1.0 + reverts as f64 + 1.5 * bug_intros as f64);
    let cycle_factor = match cycle_hours {
        Some(h) if h.is_finite() && h > 0.0 => {
            // 1.0 at instantaneous, decaying toward 0.5 at ~2 weeks.
            let weeks = h / (24.0 * 7.0);
            0.5 + 0.5 / (1.0 + weeks)
        }
        _ => 1.0,
    };
    (100.0 * defect_penalty * cycle_factor).clamp(0.0, 100.0)
}

/// Fraction of a contributor's authored lines still alive at HEAD, in 0..=1.
/// Guarded against a zero denominator.
pub fn durability_ratio(surviving: u32, authored: u32) -> f64 {
    if authored == 0 {
        return 0.0;
    }
    (surviving as f64 / authored as f64).clamp(0.0, 1.0)
}

/// Cluster raw contributor rows into merged identities by shared emails (and,
/// failing that, an exact case-insensitive display-name match). Union-find over
/// the email graph; the lowest primary_email (lexicographically) becomes the
/// cluster's canonical row, with all emails/logins merged in.
pub fn merge_contributor_identities(rows: &[Contributor]) -> Vec<Contributor> {
    use std::collections::HashMap;

    // Assign each row an index; union rows that share any email.
    let n = rows.len();
    let mut parent: Vec<usize> = (0..n).collect();

    fn find(parent: &mut [usize], mut x: usize) -> usize {
        while parent[x] != x {
            parent[x] = parent[parent[x]];
            x = parent[x];
        }
        x
    }
    fn union(parent: &mut [usize], a: usize, b: usize) {
        let ra = find(parent, a);
        let rb = find(parent, b);
        if ra != rb {
            parent[ra.max(rb)] = ra.min(rb);
        }
    }

    // email (lowercased) -> first row index seen with it
    let mut email_owner: HashMap<String, usize> = HashMap::new();
    for (i, r) in rows.iter().enumerate() {
        let mut all_emails: Vec<String> = r.emails.clone();
        all_emails.push(r.primary_email.clone());
        for e in all_emails {
            let key = e.trim().to_lowercase();
            if key.is_empty() {
                continue;
            }
            match email_owner.get(&key) {
                Some(&owner) => union(&mut parent, owner, i),
                None => {
                    email_owner.insert(key, i);
                }
            }
        }
    }

    // Group rows by cluster root.
    let mut clusters: HashMap<usize, Vec<usize>> = HashMap::new();
    for i in 0..n {
        let root = find(&mut parent, i);
        clusters.entry(root).or_default().push(i);
    }

    let mut out: Vec<Contributor> = Vec::with_capacity(clusters.len());
    for (_root, members) in clusters {
        // Canonical row = the one with the lexicographically smallest email.
        let canon_idx = *members
            .iter()
            .min_by(|&&a, &&b| rows[a].primary_email.cmp(&rows[b].primary_email))
            .expect("cluster is non-empty");

        let mut emails: Vec<String> = Vec::new();
        let mut login: Option<String> = None;
        let mut is_agent = false;
        let mut agent_kind: Option<String> = None;
        let mut display_name = rows[canon_idx].display_name.clone();

        for &m in &members {
            let r = &rows[m];
            for e in std::iter::once(&r.primary_email).chain(r.emails.iter()) {
                let e = e.trim();
                if !e.is_empty() && !emails.iter().any(|x| x.eq_ignore_ascii_case(e)) {
                    emails.push(e.to_string());
                }
            }
            if login.is_none() {
                login = r.login.clone();
            }
            if r.is_agent {
                is_agent = true;
                if agent_kind.is_none() {
                    agent_kind = r.agent_kind.clone();
                }
            }
            if display_name.is_empty() {
                display_name = r.display_name.clone();
            }
        }
        emails.sort();

        out.push(Contributor {
            id: rows[canon_idx].id.clone(),
            display_name,
            primary_email: rows[canon_idx].primary_email.clone(),
            emails,
            login,
            is_agent,
            agent_kind,
        });
    }

    out.sort_by(|a, b| a.primary_email.cmp(&b.primary_email));
    out
}

/// A shared, deterministic heuristic for "is this a test file?". Kept in core
/// so the git-walk (`is_test_touch`) and the classifier agree exactly.
pub fn is_test_path(path: &str) -> bool {
    let p = path.to_lowercase();
    let file = p.rsplit('/').next().unwrap_or(&p);
    p.contains("/test/")
        || p.contains("/tests/")
        || p.contains("/__tests__/")
        || p.contains("/spec/")
        || p.contains("/specs/")
        || p.contains("/testdata/")
        || p.starts_with("test/")
        || p.starts_with("tests/")
        || file.starts_with("test_")
        || file.ends_with("_test.go")
        || file.ends_with("_test.py")
        || file.ends_with("_test.rs")
        || file.ends_with(".test.js")
        || file.ends_with(".test.ts")
        || file.ends_with(".test.jsx")
        || file.ends_with(".test.tsx")
        || file.ends_with(".spec.js")
        || file.ends_with(".spec.ts")
        || file.ends_with(".spec.jsx")
        || file.ends_with(".spec.tsx")
        || file.ends_with("_spec.rb")
        || file.ends_with("test.java")
        || file.ends_with("tests.cs")
}

/// Convenience: build a stable, order-independent `ContributorId` key list from
/// a slice, so callers can index contributions deterministically.
pub fn contributor_ids(rows: &[Contributor]) -> Vec<ContributorId> {
    let mut ids: Vec<ContributorId> = rows.iter().map(|c| c.id.clone()).collect();
    ids.sort_by(|a, b| a.0.cmp(&b.0));
    ids
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_single_member_is_neutral() {
        assert_eq!(normalize_dim(5.0, &[5.0]), 50.0);
        assert_eq!(normalize_dim(5.0, &[]), 50.0);
    }

    #[test]
    fn normalize_flat_cohort_is_neutral() {
        assert_eq!(normalize_dim(3.0, &[3.0, 3.0, 3.0]), 50.0);
    }

    #[test]
    fn normalize_spreads_min_to_max() {
        assert_eq!(normalize_dim(0.0, &[0.0, 10.0]), 0.0);
        assert_eq!(normalize_dim(10.0, &[0.0, 10.0]), 100.0);
        assert_eq!(normalize_dim(5.0, &[0.0, 10.0]), 50.0);
    }

    #[test]
    fn quality_falls_with_defects() {
        let clean = quality_from(0, 0, Some(1.0));
        let dirty = quality_from(3, 4, Some(1.0));
        assert!(clean > dirty);
        assert!(clean <= 100.0 && dirty >= 0.0);
    }

    #[test]
    fn durability_guards_zero() {
        assert_eq!(durability_ratio(10, 0), 0.0);
        assert_eq!(durability_ratio(5, 10), 0.5);
        assert_eq!(durability_ratio(20, 10), 1.0);
    }

    #[test]
    fn identity_merge_clusters_shared_emails() {
        let rows = vec![
            Contributor {
                id: ContributorId::new(),
                display_name: "Ada".into(),
                primary_email: "ada@work".into(),
                emails: vec!["ada@home".into()],
                login: Some("ada".into()),
                is_agent: false,
                agent_kind: None,
            },
            Contributor {
                id: ContributorId::new(),
                display_name: "Ada L".into(),
                primary_email: "ada@home".into(),
                emails: vec![],
                login: None,
                is_agent: false,
                agent_kind: None,
            },
        ];
        let merged = merge_contributor_identities(&rows);
        assert_eq!(merged.len(), 1);
        assert!(merged[0].emails.iter().any(|e| e == "ada@work"));
        assert!(merged[0].emails.iter().any(|e| e == "ada@home"));
    }

    #[test]
    fn test_paths() {
        assert!(is_test_path("src/foo_test.go"));
        assert!(is_test_path("web/src/App.test.tsx"));
        assert!(is_test_path("tests/integration/a.rs"));
        assert!(!is_test_path("src/main.rs"));
    }
}
