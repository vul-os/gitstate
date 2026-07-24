//! The neutral shape every tracker maps into, and its conversion to a
//! [`WorkItem`].
//!
//! Jira and Linear model status very differently (Jira has per-project custom
//! workflows; Linear has typed states), so neither vendor's *status name* is
//! trustworthy. Both, however, expose a small fixed **category** — Jira's
//! `statusCategory.key` and Linear's `state.type` — and that is what we map
//! from. A team that renamed "Done" to "Shipped" still imports correctly.

use gitstate_core::{RepoId, WorkItem, WorkItemId, WorkKind, WorkState};
use serde::{Deserialize, Serialize};

/// A tracker issue, normalized across vendors.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ImportedItem {
    /// Which tracker it came from ("jira" / "linear").
    pub source: String,
    /// The human key: `ENG-412`.
    pub key: String,
    pub title: String,
    #[serde(default)]
    pub body: String,
    pub state: WorkState,
    #[serde(default)]
    pub author: Option<String>,
    #[serde(default)]
    pub labels: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    /// When it reached a terminal state, if it has.
    #[serde(default)]
    pub closed_at: Option<String>,
    /// Vendor deep link, so the UI can send you back to the source of truth.
    #[serde(default)]
    pub url: Option<String>,
}

impl ImportedItem {
    /// Attach to a repo as a storable [`WorkItem`].
    ///
    /// Imported issues are always [`WorkKind::Issue`]: they are tracker tickets,
    /// never pull requests, and conflating the two would corrupt the cycle-time
    /// series (which measures open→merge on PRs specifically).
    pub fn to_work_item(&self, repo: &RepoId) -> WorkItem {
        WorkItem {
            id: stable_item_id(&self.source, &self.key),
            repo_id: repo.clone(),
            kind: WorkKind::Issue,
            external_ref: self.key.clone(),
            title: self.title.clone(),
            body: self.body.clone(),
            state: self.state,
            author_login: self.author.clone(),
            labels: self.labels.clone(),
            created_at: self.created_at.clone(),
            updated_at: self.updated_at.clone(),
            merged_at: None,
            closed_at: self.closed_at.clone(),
            files_touched: Vec::new(),
        }
    }
}

/// A deterministic id for an imported issue, so re-importing **updates in
/// place** instead of accumulating duplicates every time you sync.
pub fn stable_item_id(source: &str, key: &str) -> WorkItemId {
    WorkItemId::from(format!("{}:{}", source.to_ascii_lowercase(), key))
}

/// Map Jira's `statusCategory.key` — the only status field that is stable
/// across custom workflows.
pub(crate) fn state_from_jira_category(key: &str) -> WorkState {
    match key.to_ascii_lowercase().as_str() {
        "done" => WorkState::Done,
        "indeterminate" => WorkState::InProgress,
        // "new", and anything a future Jira adds, is untouched work.
        _ => WorkState::Open,
    }
}

/// Map Linear's `state.type`, which is a closed vendor enum.
pub(crate) fn state_from_linear_type(ty: &str) -> WorkState {
    match ty.to_ascii_lowercase().as_str() {
        "completed" => WorkState::Done,
        "canceled" | "cancelled" => WorkState::Closed,
        "started" => WorkState::InProgress,
        // "backlog" | "triage" | "unstarted"
        _ => WorkState::Open,
    }
}

/// Flatten Atlassian Document Format into plain text.
///
/// Jira REST v3 returns rich text as a nested ADF tree, not a string. The
/// classifier only ever reads prose, so we walk the tree and concatenate its
/// `text` leaves rather than pulling in a full ADF renderer. Paragraph and
/// hard-break nodes become newlines so sentences don't run together.
pub(crate) fn adf_to_text(node: &serde_json::Value) -> String {
    let mut out = String::new();
    walk_adf(node, &mut out);
    out.trim().to_string()
}

fn walk_adf(node: &serde_json::Value, out: &mut String) {
    match node {
        serde_json::Value::String(s) => out.push_str(s),
        serde_json::Value::Array(items) => {
            for it in items {
                walk_adf(it, out);
            }
        }
        serde_json::Value::Object(map) => {
            if let Some(serde_json::Value::String(t)) = map.get("text") {
                out.push_str(t);
            }
            if let Some(content) = map.get("content") {
                walk_adf(content, out);
            }
            match map.get("type").and_then(|t| t.as_str()) {
                Some("paragraph") | Some("heading") | Some("listItem") | Some("hardBreak") => {
                    out.push('\n')
                }
                _ => {}
            }
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn jira_status_categories_map_by_category_not_name() {
        // A team that renamed its columns still imports correctly, because we
        // read the category rather than the display name.
        assert_eq!(state_from_jira_category("done"), WorkState::Done);
        assert_eq!(state_from_jira_category("Done"), WorkState::Done);
        assert_eq!(
            state_from_jira_category("indeterminate"),
            WorkState::InProgress
        );
        assert_eq!(state_from_jira_category("new"), WorkState::Open);
        // An unknown future category degrades to Open rather than panicking.
        assert_eq!(state_from_jira_category("something-new"), WorkState::Open);
    }

    #[test]
    fn linear_state_types_map_including_cancelled() {
        assert_eq!(state_from_linear_type("completed"), WorkState::Done);
        assert_eq!(state_from_linear_type("started"), WorkState::InProgress);
        assert_eq!(state_from_linear_type("backlog"), WorkState::Open);
        assert_eq!(state_from_linear_type("unstarted"), WorkState::Open);
        // Cancelled is closed-but-not-done: it must not inflate "done" counts.
        assert_eq!(state_from_linear_type("canceled"), WorkState::Closed);
        assert_eq!(state_from_linear_type("cancelled"), WorkState::Closed);
    }

    #[test]
    fn adf_flattens_to_prose() {
        let doc = json!({
            "type": "doc",
            "content": [
                { "type": "paragraph", "content": [{ "type": "text", "text": "Search is stale." }] },
                { "type": "paragraph", "content": [
                    { "type": "text", "text": "Repro: " },
                    { "type": "text", "text": "hit /search twice." }
                ]}
            ]
        });
        let text = adf_to_text(&doc);
        assert!(text.contains("Search is stale."));
        assert!(text.contains("Repro: hit /search twice."));
        // Paragraphs separate rather than running together.
        assert!(text.contains('\n'));
    }

    #[test]
    fn adf_handles_null_and_plain_string_bodies() {
        assert_eq!(adf_to_text(&serde_json::Value::Null), "");
        assert_eq!(adf_to_text(&json!("already plain")), "already plain");
    }

    #[test]
    fn imported_ids_are_stable_so_reimport_updates_in_place() {
        let a = stable_item_id("jira", "ENG-1");
        let b = stable_item_id("JIRA", "ENG-1");
        assert_eq!(a, b, "case in the source must not fork the id");
        assert_ne!(a, stable_item_id("linear", "ENG-1"));
        assert_ne!(a, stable_item_id("jira", "ENG-2"));
    }

    #[test]
    fn imported_items_become_issues_never_pull_requests() {
        let item = ImportedItem {
            source: "linear".into(),
            key: "ENG-9".into(),
            title: "Fix the thing".into(),
            body: "details".into(),
            state: WorkState::Done,
            author: Some("ada".into()),
            labels: vec!["bug".into()],
            created_at: "2026-06-01T00:00:00Z".into(),
            updated_at: "2026-06-02T00:00:00Z".into(),
            closed_at: Some("2026-06-02T00:00:00Z".into()),
            url: Some("https://linear.app/x/issue/ENG-9".into()),
        };
        let wi = item.to_work_item(&RepoId::from("r1"));
        assert_eq!(wi.kind, WorkKind::Issue);
        // Never a PR: cycle time measures open→merge on PRs and would be
        // corrupted by tracker tickets landing in that series.
        assert!(wi.merged_at.is_none());
        assert_eq!(wi.external_ref, "ENG-9");
        assert_eq!(wi.state, WorkState::Done);
        assert_eq!(wi.labels, vec!["bug".to_string()]);
    }
}
