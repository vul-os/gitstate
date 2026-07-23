//! The deterministic, always-available classifier. Keyword + path + label
//! rules that map a work item onto a taxonomy key. No network, fully
//! reproducible — this is the fallback whenever no LLM endpoint is configured.

use async_trait::async_trait;
use gitstate_core::derive::is_test_path;
use gitstate_core::{
    Classification, Classifier, ClassifierCapability, DiffSummary, EffortEstimate, EffortMethod,
    Result, Taxonomy, WorkItem,
};

use crate::effort::heuristic_effort;

#[async_trait]
impl Classifier for HeuristicClassifier {
    fn capability(&self) -> ClassifierCapability {
        ClassifierCapability::Heuristic
    }
    async fn classify(
        &self,
        items: &[WorkItem],
        taxonomy: &Taxonomy,
    ) -> Result<Vec<Classification>> {
        Ok(classify_all(self, items, taxonomy))
    }
    async fn judge_effort(&self, diffs: &[DiffSummary]) -> Result<Vec<EffortEstimate>> {
        Ok(effort_all(diffs))
    }
}

/// The always-available deterministic classifier.
pub struct HeuristicClassifier;

impl HeuristicClassifier {
    pub fn new() -> Self {
        HeuristicClassifier
    }

    /// Classify a single item against the taxonomy's key set.
    pub fn classify_one(&self, item: &WorkItem, taxonomy: &Taxonomy) -> Classification {
        let keys: Vec<&str> = taxonomy.categories.iter().map(|c| c.key.as_str()).collect();
        let has = |k: &str| keys.contains(&k);
        let fallback = if has("chore") {
            "chore"
        } else {
            keys.first().copied().unwrap_or("chore")
        };

        let hay = format!(
            "{} {} {}",
            item.title.to_lowercase(),
            item.body.to_lowercase(),
            item.labels.join(" ").to_lowercase()
        );
        let labels_lc: Vec<String> = item.labels.iter().map(|l| l.to_lowercase()).collect();

        // 1. Explicit labels win.
        for l in &labels_lc {
            if let Some(k) = label_to_key(l) {
                if has(k) {
                    return mk(item, k, 0.9, format!("matched label '{l}'"));
                }
            }
        }

        // 2. Strong keyword signals, most specific first.
        let ordered: &[(&str, &[&str])] = &[
            ("revert", &["revert", "rollback", "roll back"]),
            (
                "hotfix",
                &["hotfix", "incident", "sev1", "sev0", "p0", "outage"],
            ),
            (
                "security",
                &[
                    "security",
                    "vuln",
                    "cve",
                    "xss",
                    "csrf",
                    "sql injection",
                    "auth bypass",
                ],
            ),
            (
                "perf",
                &[
                    "perf",
                    "performance",
                    "latency",
                    "throughput",
                    "optimize",
                    "optimise",
                    "speed up",
                ],
            ),
            (
                "release",
                &["release", "bump version", "changelog", "v0.", "v1.", "tag "],
            ),
            (
                "deps",
                &[
                    "dependency",
                    "dependencies",
                    "bump",
                    "upgrade dep",
                    "dependabot",
                    "renovate",
                ],
            ),
            (
                "ci",
                &["ci", "pipeline", "github actions", "workflow", "gitlab-ci"],
            ),
            (
                "build",
                &["build", "packaging", "makefile", "dockerfile", "compile"],
            ),
            ("docs", &["docs", "documentation", "readme", "typo in doc"]),
            ("test", &["test", "tests", "unit test", "e2e", "coverage"]),
            (
                "refactor",
                &[
                    "refactor",
                    "cleanup",
                    "clean up",
                    "restructure",
                    "simplify",
                    "rename",
                ],
            ),
            (
                "bugfix",
                &[
                    "fix",
                    "bug",
                    "regression",
                    "broken",
                    "crash",
                    "npe",
                    "panic",
                ],
            ),
            (
                "feature.api",
                &["api", "endpoint", "route", "handler", "grpc", "graphql"],
            ),
            (
                "feature.ui",
                &[
                    "ui",
                    "frontend",
                    "css",
                    "component",
                    "button",
                    "page",
                    "styling",
                ],
            ),
            (
                "feature.data",
                &[
                    "schema",
                    "migration",
                    "database",
                    "table",
                    "storage",
                    "index",
                ],
            ),
            ("feature-flag", &["feature flag", "flag", "rollout", "gate"]),
            (
                "config",
                &["config", "configuration", "settings", "env var"],
            ),
            (
                "infra",
                &[
                    "infra",
                    "infrastructure",
                    "terraform",
                    "kubernetes",
                    "deployment",
                    "ops",
                ],
            ),
            (
                "feature",
                &["add", "implement", "feature", "support", "introduce", "new"],
            ),
        ];
        for (key, needles) in ordered {
            if has(key) && needles.iter().any(|n| hay.contains(n)) {
                return mk(item, key, 0.7, format!("keyword match for '{key}'"));
            }
        }

        // 3. File-path signals.
        if !item.files_touched.is_empty() {
            let all_tests = item.files_touched.iter().all(|f| is_test_path(f));
            if all_tests && has("test") {
                return mk(item, "test", 0.75, "all touched files are tests".into());
            }
            if item.files_touched.iter().all(|f| is_docs_path(f)) && has("docs") {
                return mk(item, "docs", 0.75, "all touched files are docs".into());
            }
            if item.files_touched.iter().any(|f| is_ci_path(f)) && has("ci") {
                return mk(item, "ci", 0.6, "touches CI configuration".into());
            }
        }

        mk(item, fallback, 0.3, "no strong signal; defaulted".into())
    }
}

impl Default for HeuristicClassifier {
    fn default() -> Self {
        HeuristicClassifier::new()
    }
}

fn mk(item: &WorkItem, key: &str, confidence: f64, rationale: String) -> Classification {
    Classification {
        item_id: item.id.clone(),
        category_key: key.to_string(),
        confidence,
        method: EffortMethod::Heuristic,
        rationale,
    }
}

/// Batch classify.
pub fn classify_all(
    hc: &HeuristicClassifier,
    items: &[WorkItem],
    taxonomy: &Taxonomy,
) -> Vec<Classification> {
    items.iter().map(|i| hc.classify_one(i, taxonomy)).collect()
}

/// Batch effort via the deterministic heuristic.
pub fn effort_all(diffs: &[DiffSummary]) -> Vec<EffortEstimate> {
    diffs.iter().map(heuristic_effort).collect()
}

fn label_to_key(label: &str) -> Option<&'static str> {
    let l = label.trim();
    Some(match l {
        "bug" | "bugfix" | "type: bug" | "kind/bug" => "bugfix",
        "hotfix" | "incident" => "hotfix",
        "security" => "security",
        "performance" | "perf" => "perf",
        "enhancement" | "feature" | "type: feature" => "feature",
        "documentation" | "docs" => "docs",
        "test" | "tests" | "testing" => "test",
        "refactor" | "refactoring" | "tech-debt" => "refactor",
        "dependencies" | "deps" => "deps",
        "ci" | "ci/cd" => "ci",
        "build" => "build",
        "chore" => "chore",
        "revert" => "revert",
        "release" => "release",
        "infra" | "infrastructure" => "infra",
        "config" | "configuration" => "config",
        _ => return None,
    })
}

fn is_docs_path(p: &str) -> bool {
    let l = p.to_lowercase();
    l.ends_with(".md")
        || l.ends_with(".mdx")
        || l.ends_with(".rst")
        || l.starts_with("docs/")
        || l.contains("/docs/")
}

fn is_ci_path(p: &str) -> bool {
    let l = p.to_lowercase();
    l.contains(".github/workflows/")
        || l.ends_with(".gitlab-ci.yml")
        || l.contains("/ci/")
        || l.contains("azure-pipelines")
}
