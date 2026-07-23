//! gitstate-classify — the [`Classifier`] seam.
//!
//! Two implementations, one contract:
//! * [`LlmClassifier`] — any OpenAI-compatible endpoint (llmux, a local model),
//!   configured from the environment; falls back to the heuristic on failure.
//! * [`HeuristicClassifier`] — deterministic keyword/path/label rules; always
//!   available, no network.
//!
//! Plus [`Personalizer`] (local, per-box learning that replaces pooled
//! fine-tuning) and [`load_and_verify_taxonomy`] (fail-closed signed-taxonomy
//! loading). Personal categorization stays LOCAL — better for privacy.

mod effort;
mod heuristic;
mod llm;
mod personalize;
mod taxonomy_verify;

pub use effort::heuristic_effort;
pub use heuristic::{
    classify_all as heuristic_classify_all, effort_all as heuristic_effort_all, HeuristicClassifier,
};
pub use llm::LlmClassifier;
pub use personalize::Personalizer;
pub use taxonomy_verify::{load_and_verify_taxonomy, pinned_pubkey};

use gitstate_core::Classifier;

/// The default classifier: LLM when an endpoint is configured in the
/// environment, otherwise the deterministic heuristic.
pub fn default_classifier() -> Box<dyn Classifier> {
    match LlmClassifier::from_env() {
        Some(llm) => Box::new(llm),
        None => Box::new(HeuristicClassifier::new()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{Classifier, RepoId, Taxonomy, WorkItem, WorkItemId, WorkKind, WorkState};

    fn item(title: &str, labels: &[&str], files: &[&str]) -> WorkItem {
        WorkItem {
            id: WorkItemId::new(),
            repo_id: RepoId::new(),
            kind: WorkKind::Pr,
            external_ref: "#1".into(),
            title: title.into(),
            body: String::new(),
            state: WorkState::Merged,
            author_login: None,
            labels: labels.iter().map(|s| s.to_string()).collect(),
            created_at: "2026-01-01T00:00:00Z".into(),
            updated_at: "2026-01-01T00:00:00Z".into(),
            merged_at: None,
            closed_at: None,
            files_touched: files.iter().map(|s| s.to_string()).collect(),
        }
    }

    #[tokio::test]
    async fn heuristic_classifies_deterministically() {
        let tx = Taxonomy::default_taxonomy();
        let hc = HeuristicClassifier::new();
        let items = vec![
            item("Fix crash on startup", &[], &[]),
            item("Add REST endpoint for repos", &[], &[]),
            item("Update README", &["documentation"], &[]),
        ];
        let out = hc.classify(&items, &tx).await.unwrap();
        assert_eq!(out[0].category_key, "bugfix");
        assert_eq!(out[1].category_key, "feature.api");
        assert_eq!(out[2].category_key, "docs");
    }

    #[test]
    fn default_is_heuristic_without_env() {
        // In a clean env (no VULOS_LLMUX_URL / OPENAI_BASE_URL) this is heuristic.
        if std::env::var("VULOS_LLMUX_URL").is_err() && std::env::var("OPENAI_BASE_URL").is_err() {
            let c = default_classifier();
            assert_eq!(
                c.capability(),
                gitstate_core::ClassifierCapability::Heuristic
            );
        }
    }
}
