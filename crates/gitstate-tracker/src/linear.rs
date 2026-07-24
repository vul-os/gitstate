//! Linear client — the public GraphQL API, authenticated with **your**
//! personal API key, straight from this machine.
//!
//! Linear personal keys go in `Authorization` *without* a `Bearer` prefix
//! (unlike OAuth access tokens), which is the usual reason a hand-rolled Linear
//! integration 400s.

use async_trait::async_trait;
use gitstate_core::{Error, Result};
use serde::Deserialize;

use crate::map::{state_from_linear_type, ImportedItem};
use crate::{http, TrackerClient, TrackerConfig, TrackerKind, TrackerStatus};

const ENDPOINT: &str = "https://api.linear.app/graphql";
/// Linear caps a page at 250; 100 keeps each response small and responsive.
const PAGE: usize = 100;

const ISSUES_QUERY: &str = r#"
query Issues($first: Int!, $after: String, $filter: IssueFilter) {
  issues(first: $first, after: $after, filter: $filter, orderBy: updatedAt) {
    pageInfo { hasNextPage endCursor }
    nodes {
      identifier
      title
      description
      createdAt
      updatedAt
      completedAt
      canceledAt
      url
      state { name type }
      labels { nodes { name } }
      assignee { displayName email }
      creator { displayName email }
    }
  }
}"#;

const VIEWER_QUERY: &str = "query { viewer { name email } }";

pub struct LinearClient {
    token: String,
    team: String,
}

impl LinearClient {
    pub fn new(cfg: &TrackerConfig) -> Self {
        LinearClient {
            token: cfg.token.clone(),
            team: cfg.project.trim().to_string(),
        }
    }

    async fn post(&self, body: serde_json::Value) -> Result<serde_json::Value> {
        let resp = http()
            .post(ENDPOINT)
            // A personal API key is sent bare — prefixing it with "Bearer"
            // makes Linear reject the request.
            .header("Authorization", &self.token)
            .header("Content-Type", "application/json")
            .json(&body)
            .send()
            .await
            .map_err(Error::http)?;

        let status = resp.status();
        let value: serde_json::Value = resp.json().await.map_err(Error::http)?;

        // GraphQL reports failures in the body with a 200, so check both.
        if let Some(errors) = value.get("errors").and_then(|e| e.as_array()) {
            if !errors.is_empty() {
                let msg = errors
                    .iter()
                    .filter_map(|e| e.get("message").and_then(|m| m.as_str()))
                    .collect::<Vec<_>>()
                    .join("; ");
                return Err(Error::forge(format!("linear: {msg}")));
            }
        }
        if !status.is_success() {
            return Err(Error::forge(format!("linear responded {status}")));
        }
        Ok(value)
    }

    /// Scope to a team by key when one is configured.
    fn filter(&self) -> serde_json::Value {
        if self.team.is_empty() {
            serde_json::Value::Null
        } else {
            serde_json::json!({ "team": { "key": { "eq": self.team } } })
        }
    }
}

#[derive(Deserialize)]
struct IssuesData {
    issues: IssueConnection,
}

#[derive(Deserialize)]
struct IssueConnection {
    #[serde(default, rename = "pageInfo")]
    page_info: PageInfo,
    #[serde(default)]
    nodes: Vec<Node>,
}

#[derive(Default, Deserialize)]
struct PageInfo {
    #[serde(default, rename = "hasNextPage")]
    has_next_page: bool,
    #[serde(default, rename = "endCursor")]
    end_cursor: Option<String>,
}

#[derive(Deserialize)]
struct Node {
    #[serde(default)]
    identifier: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: Option<String>,
    #[serde(default, rename = "createdAt")]
    created_at: String,
    #[serde(default, rename = "updatedAt")]
    updated_at: String,
    #[serde(default, rename = "completedAt")]
    completed_at: Option<String>,
    #[serde(default, rename = "canceledAt")]
    canceled_at: Option<String>,
    #[serde(default)]
    url: Option<String>,
    #[serde(default)]
    state: Option<State>,
    #[serde(default)]
    labels: Option<LabelConnection>,
    #[serde(default)]
    assignee: Option<Person>,
    #[serde(default)]
    creator: Option<Person>,
}

#[derive(Deserialize)]
struct State {
    #[serde(default)]
    #[serde(rename = "type")]
    ty: String,
}

#[derive(Default, Deserialize)]
struct LabelConnection {
    #[serde(default)]
    nodes: Vec<Label>,
}

#[derive(Deserialize)]
struct Label {
    #[serde(default)]
    name: String,
}

#[derive(Deserialize)]
struct Person {
    #[serde(default)]
    name: String,
    #[serde(default)]
    #[serde(rename = "displayName")]
    display_name: Option<String>,
    #[serde(default)]
    email: Option<String>,
}

impl Person {
    fn label(self) -> String {
        self.email
            .filter(|e| !e.is_empty())
            .or(self.display_name.filter(|d| !d.is_empty()))
            .unwrap_or(self.name)
    }
}

#[derive(Deserialize)]
struct ViewerData {
    viewer: Person,
}

#[async_trait]
impl TrackerClient for LinearClient {
    fn kind(&self) -> TrackerKind {
        TrackerKind::Linear
    }

    async fn test(&self) -> Result<TrackerStatus> {
        match self
            .post(serde_json::json!({ "query": VIEWER_QUERY }))
            .await
        {
            Ok(v) => {
                let data: ViewerData = serde_json::from_value(v["data"].clone())
                    .map_err(|e| Error::forge(format!("linear viewer: {e}")))?;
                let account = data.viewer.label();
                Ok(TrackerStatus {
                    ok: true,
                    message: format!("Connected to Linear as {account}"),
                    account: Some(account),
                })
            }
            Err(e) => Ok(TrackerStatus {
                ok: false,
                account: None,
                message: format!("{e} — check the personal API key (Linear > Settings > API)."),
            }),
        }
    }

    async fn fetch(&self, limit: usize) -> Result<Vec<ImportedItem>> {
        let mut out: Vec<ImportedItem> = Vec::new();
        let mut cursor: Option<String> = None;

        while out.len() < limit {
            let want = PAGE.min(limit - out.len());
            let vars = serde_json::json!({
                "first": want,
                "after": cursor,
                "filter": self.filter(),
            });
            let body = serde_json::json!({ "query": ISSUES_QUERY, "variables": vars });
            let value = self.post(body).await?;

            let data: IssuesData = serde_json::from_value(value["data"].clone())
                .map_err(|e| Error::forge(format!("linear issues: {e}")))?;
            let got = data.issues.nodes.len();

            for n in data.issues.nodes {
                let ty = n.state.map(|s| s.ty).unwrap_or_default();
                // Prefer the completion timestamp, but a cancelled issue is
                // also terminal and should carry its own closed_at.
                let closed_at = n.completed_at.or(n.canceled_at);
                let author = n.assignee.or(n.creator).map(Person::label);

                out.push(ImportedItem {
                    source: "linear".into(),
                    key: n.identifier,
                    title: n.title,
                    body: n.description.unwrap_or_default(),
                    state: state_from_linear_type(&ty),
                    author,
                    labels: n
                        .labels
                        .map(|l| l.nodes.into_iter().map(|x| x.name).collect())
                        .unwrap_or_default(),
                    created_at: n.created_at,
                    updated_at: n.updated_at,
                    closed_at,
                    url: n.url,
                });
            }

            if !data.issues.page_info.has_next_page || got == 0 {
                break;
            }
            cursor = data.issues.page_info.end_cursor;
            if cursor.is_none() {
                break;
            }
        }

        out.truncate(limit);
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn filter_scopes_by_team_key() {
        let c = LinearClient::new(&TrackerConfig {
            token: "lin_api_x".into(),
            project: "ENG".into(),
            ..Default::default()
        });
        assert_eq!(c.filter(), serde_json::json!({"team":{"key":{"eq":"ENG"}}}));

        let all = LinearClient::new(&TrackerConfig {
            token: "lin_api_x".into(),
            ..Default::default()
        });
        assert!(
            all.filter().is_null(),
            "no team ⇒ import everything visible"
        );
    }

    #[test]
    fn parses_a_realistic_issues_page() {
        let raw = serde_json::json!({
            "issues": {
                "pageInfo": { "hasNextPage": true, "endCursor": "cur1" },
                "nodes": [{
                    "identifier": "ENG-412",
                    "title": "Search returns stale results",
                    "description": "Cache is not invalidated.",
                    "createdAt": "2026-05-01T09:00:00.000Z",
                    "updatedAt": "2026-06-01T09:00:00.000Z",
                    "completedAt": null,
                    "canceledAt": null,
                    "url": "https://linear.app/acme/issue/ENG-412",
                    "state": { "name": "In Progress", "type": "started" },
                    "labels": { "nodes": [{ "name": "bug" }, { "name": "search" }] },
                    "assignee": { "name": "Ada", "displayName": "Ada K", "email": "ada@example.com" }
                }]
            }
        });
        let data: IssuesData = serde_json::from_value(raw).unwrap();
        assert!(data.issues.page_info.has_next_page);
        assert_eq!(data.issues.page_info.end_cursor.as_deref(), Some("cur1"));

        let n = &data.issues.nodes[0];
        assert_eq!(n.identifier, "ENG-412");
        assert_eq!(
            state_from_linear_type(&n.state.as_ref().unwrap().ty),
            gitstate_core::WorkState::InProgress
        );
        assert_eq!(n.labels.as_ref().unwrap().nodes.len(), 2);
    }

    #[test]
    fn person_prefers_email_then_display_name() {
        let with_email = Person {
            name: "n".into(),
            display_name: Some("d".into()),
            email: Some("e@x".into()),
        };
        assert_eq!(with_email.label(), "e@x");

        let no_email = Person {
            name: "n".into(),
            display_name: Some("d".into()),
            email: None,
        };
        assert_eq!(no_email.label(), "d");

        let bare = Person {
            name: "n".into(),
            display_name: None,
            email: Some(String::new()),
        };
        assert_eq!(bare.label(), "n");
    }

    #[test]
    fn tolerates_a_sparse_node() {
        let data: IssuesData = serde_json::from_value(serde_json::json!({
            "issues": { "nodes": [{ "identifier": "X-1" }] }
        }))
        .unwrap();
        let n = &data.issues.nodes[0];
        assert!(n.state.is_none());
        assert!(n.labels.is_none());
        assert!(n.assignee.is_none());
        assert!(!data.issues.page_info.has_next_page);
    }
}
