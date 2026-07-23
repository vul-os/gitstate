//! Deterministic effort judging: a diff's *difficulty* (fibonacci-ish, NOT a
//! line count) derived from its churn, file spread, and language breadth. Used
//! as the LLM's fallback and as the standalone heuristic.

use gitstate_core::{DiffSummary, EffortEstimate, EffortMethod};

const FIB: [f64; 6] = [1.0, 2.0, 3.0, 5.0, 8.0, 13.0];

/// Map a diff shape onto a difficulty point on the fibonacci scale.
pub fn heuristic_effort(d: &DiffSummary) -> EffortEstimate {
    let churn = d.additions as u64 + d.deletions as u64;
    let files = d.files.max(1);
    let langs = d.languages.len().max(1) as u64;

    // Base bucket from churn.
    let mut idx = match churn {
        0..=9 => 0,
        10..=49 => 1,
        50..=149 => 2,
        150..=399 => 3,
        400..=999 => 4,
        _ => 5,
    };
    // Wide file spread or many languages nudge difficulty up.
    if files >= 8 || langs >= 3 {
        idx = (idx + 1).min(5);
    }
    if files >= 25 {
        idx = (idx + 1).min(5);
    }

    let difficulty = FIB[idx];
    EffortEstimate {
        item_id: d.item_id.clone(),
        difficulty,
        method: EffortMethod::Heuristic,
        rationale: format!(
            "churn {churn} across {files} file(s), {langs} language(s) → {difficulty} pts",
        ),
        confidence: 0.5,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::WorkItemId;

    fn diff(add: u32, del: u32, files: u32, langs: usize) -> DiffSummary {
        DiffSummary {
            item_id: WorkItemId::new(),
            external_ref: "#1".into(),
            additions: add,
            deletions: del,
            files,
            languages: (0..langs).map(|i| format!("lang{i}")).collect(),
            touched_paths: vec![],
            title: "t".into(),
            body: "b".into(),
        }
    }

    #[test]
    fn tiny_change_is_one_point() {
        assert_eq!(heuristic_effort(&diff(2, 1, 1, 1)).difficulty, 1.0);
    }

    #[test]
    fn large_change_caps_at_thirteen() {
        assert_eq!(heuristic_effort(&diff(5000, 2000, 60, 4)).difficulty, 13.0);
    }

    #[test]
    fn spread_bumps_difficulty() {
        let narrow = heuristic_effort(&diff(30, 0, 1, 1)).difficulty;
        let wide = heuristic_effort(&diff(30, 0, 10, 3)).difficulty;
        assert!(wide > narrow);
    }
}
