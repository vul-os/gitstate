//! Jira Cloud client — REST v3, authenticated with **your** Atlassian account
//! email + a personal API token (Basic auth), straight from this machine.
//!
//! Uses the token-paginated `/rest/api/3/search/jql` endpoint; the older
//! offset-paginated `/rest/api/3/search` was deprecated and removed on Cloud.

use async_trait::async_trait;
use gitstate_core::{Error, Result};
use serde::Deserialize;

use crate::map::{adf_to_text, state_from_jira_category, ImportedItem};
use crate::{http, TrackerClient, TrackerConfig, TrackerKind, TrackerStatus};

/// Jira caps `maxResults` at 100 for search.
const PAGE: usize = 100;

pub struct JiraClient {
    base: String,
    email: String,
    token: String,
    project: String,
}

impl JiraClient {
    pub fn new(cfg: &TrackerConfig) -> Result<Self> {
        let base = cfg.base_url.trim().trim_end_matches('/').to_string();
        if base.is_empty() {
            return Err(Error::invalid(
                "jira needs a site URL, e.g. https://your-site.atlassian.net",
            ));
        }
        if !base.starts_with("https://") && !base.starts_with("http://") {
            return Err(Error::invalid(format!(
                "jira site URL must start with https:// (got {base})"
            )));
        }
        if cfg.email.trim().is_empty() {
            return Err(Error::invalid(
                "jira needs the Atlassian account email that owns the API token",
            ));
        }
        Ok(JiraClient {
            base,
            email: cfg.email.trim().to_string(),
            token: cfg.token.clone(),
            project: cfg.project.trim().to_string(),
        })
    }

    /// Scope the search, newest first. An empty project imports everything the
    /// token can see.
    fn jql(&self) -> String {
        if self.project.is_empty() {
            "ORDER BY updated DESC".to_string()
        } else {
            // Quote the key so a project like `IN` can't be read as an operator.
            format!("project = \"{}\" ORDER BY updated DESC", self.project)
        }
    }
}

#[derive(Deserialize)]
struct SearchPage {
    #[serde(default)]
    issues: Vec<Issue>,
    #[serde(default)]
    #[serde(rename = "nextPageToken")]
    next_page_token: Option<String>,
}

#[derive(Deserialize)]
struct Issue {
    #[serde(default)]
    key: String,
    #[serde(default)]
    fields: Fields,
}

#[derive(Default, Deserialize)]
struct Fields {
    #[serde(default)]
    summary: String,
    /// ADF document, a plain string on some instances, or absent.
    #[serde(default)]
    description: serde_json::Value,
    #[serde(default)]
    status: Option<Status>,
    #[serde(default)]
    labels: Vec<String>,
    #[serde(default)]
    created: String,
    #[serde(default)]
    updated: String,
    #[serde(default)]
    resolutiondate: Option<String>,
    #[serde(default)]
    assignee: Option<User>,
    #[serde(default)]
    reporter: Option<User>,
}

#[derive(Deserialize)]
struct Status {
    #[serde(default, rename = "statusCategory")]
    status_category: Option<StatusCategory>,
}

#[derive(Deserialize)]
struct StatusCategory {
    #[serde(default)]
    key: String,
}

#[derive(Deserialize)]
struct User {
    #[serde(default, rename = "displayName")]
    display_name: String,
    #[serde(default, rename = "emailAddress")]
    email_address: Option<String>,
}

#[derive(Deserialize)]
struct Myself {
    #[serde(default, rename = "displayName")]
    display_name: String,
    #[serde(default, rename = "emailAddress")]
    email_address: Option<String>,
}

/// Jira returns Atlassian timestamps as `2026-06-15T09:00:00.000+0000`, which
/// is *not* RFC3339 (the offset lacks a colon). Every downstream rollup parses
/// RFC3339, so normalize here rather than letting silent parse failures drop
/// items out of the cycle-time and throughput series.
fn to_rfc3339(ts: &str) -> String {
    let t = ts.trim();
    if t.is_empty() {
        return String::new();
    }
    let bytes = t.as_bytes();
    let n = bytes.len();
    // …+0000 / …-0530  →  …+00:00 / …-05:30
    if n >= 5 {
        let sign = bytes[n - 5];
        if (sign == b'+' || sign == b'-') && bytes[n - 4..].iter().all(|c| c.is_ascii_digit()) {
            // Split after sign+HH (3 chars), leaving MM — not after 2.
            return format!("{}{}:{}", &t[..n - 5], &t[n - 5..n - 2], &t[n - 2..]);
        }
    }
    t.to_string()
}

impl JiraClient {
    fn auth(&self, req: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        req.basic_auth(&self.email, Some(&self.token))
            .header("Accept", "application/json")
    }
}

#[async_trait]
impl TrackerClient for JiraClient {
    fn kind(&self) -> TrackerKind {
        TrackerKind::Jira
    }

    async fn test(&self) -> Result<TrackerStatus> {
        let url = format!("{}/rest/api/3/myself", self.base);
        let resp = self
            .auth(http().get(&url))
            .send()
            .await
            .map_err(Error::http)?;
        let status = resp.status();
        if status == reqwest::StatusCode::UNAUTHORIZED || status == reqwest::StatusCode::FORBIDDEN {
            return Ok(TrackerStatus {
                ok: false,
                account: None,
                message: "Jira rejected the credential — check the account email and API token."
                    .into(),
            });
        }
        if !status.is_success() {
            return Ok(TrackerStatus {
                ok: false,
                account: None,
                message: format!("Jira responded {status}"),
            });
        }
        let me: Myself = resp.json().await.map_err(Error::http)?;
        let account = me.email_address.unwrap_or(me.display_name);
        Ok(TrackerStatus {
            ok: true,
            message: format!("Connected to Jira as {account}"),
            account: Some(account),
        })
    }

    async fn fetch(&self, limit: usize) -> Result<Vec<ImportedItem>> {
        let mut out: Vec<ImportedItem> = Vec::new();
        let mut token: Option<String> = None;

        while out.len() < limit {
            let want = PAGE.min(limit - out.len());
            let mut req = http()
                .get(format!("{}/rest/api/3/search/jql", self.base))
                .query(&[
                    ("jql", self.jql().as_str()),
                    ("maxResults", &want.to_string()),
                    (
                        "fields",
                        "summary,description,status,labels,created,updated,resolutiondate,assignee,reporter",
                    ),
                ]);
            if let Some(t) = &token {
                req = req.query(&[("nextPageToken", t.as_str())]);
            }

            let resp = self.auth(req).send().await.map_err(Error::http)?;
            let status = resp.status();
            if !status.is_success() {
                let body = resp.text().await.unwrap_or_default();
                return Err(Error::forge(format!(
                    "jira search failed ({status}): {}",
                    body.chars().take(300).collect::<String>()
                )));
            }
            let page: SearchPage = resp.json().await.map_err(Error::http)?;
            let got = page.issues.len();

            for issue in page.issues {
                let f = issue.fields;
                let category = f
                    .status
                    .and_then(|s| s.status_category)
                    .map(|c| c.key)
                    .unwrap_or_default();
                let author = f
                    .assignee
                    .or(f.reporter)
                    .map(|u| u.email_address.unwrap_or(u.display_name));

                out.push(ImportedItem {
                    source: "jira".into(),
                    url: Some(format!("{}/browse/{}", self.base, issue.key)),
                    key: issue.key,
                    title: f.summary,
                    body: adf_to_text(&f.description),
                    state: state_from_jira_category(&category),
                    author,
                    labels: f.labels,
                    created_at: to_rfc3339(&f.created),
                    updated_at: to_rfc3339(&f.updated),
                    closed_at: f.resolutiondate.as_deref().map(to_rfc3339),
                });
            }

            // Stop on the last page, or if the server ignored our page token
            // and returned nothing (guards against an infinite loop).
            match page.next_page_token {
                Some(t) if got > 0 => token = Some(t),
                _ => break,
            }
        }

        out.truncate(limit);
        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cfg() -> TrackerConfig {
        TrackerConfig {
            base_url: "https://acme.atlassian.net".into(),
            email: "dev@example.com".into(),
            token: "tok".into(),
            project: "ENG".into(),
        }
    }

    #[test]
    fn requires_a_site_url_and_email() {
        let mut c = cfg();
        c.base_url = String::new();
        assert!(JiraClient::new(&c)
            .err()
            .unwrap()
            .to_string()
            .contains("site URL"));

        let mut c = cfg();
        c.email = String::new();
        assert!(JiraClient::new(&c)
            .err()
            .unwrap()
            .to_string()
            .contains("email"));

        let mut c = cfg();
        c.base_url = "acme.atlassian.net".into();
        assert!(JiraClient::new(&c)
            .err()
            .unwrap()
            .to_string()
            .contains("https://"));
    }

    #[test]
    fn trailing_slashes_are_normalized() {
        let mut c = cfg();
        c.base_url = "https://acme.atlassian.net/".into();
        assert_eq!(
            JiraClient::new(&c).unwrap().base,
            "https://acme.atlassian.net"
        );
    }

    #[test]
    fn jql_scopes_by_project_and_quotes_the_key() {
        let j = JiraClient::new(&cfg()).unwrap();
        assert_eq!(j.jql(), "project = \"ENG\" ORDER BY updated DESC");

        let mut c = cfg();
        c.project = String::new();
        assert_eq!(JiraClient::new(&c).unwrap().jql(), "ORDER BY updated DESC");
    }

    #[test]
    fn atlassian_timestamps_become_rfc3339() {
        // The offset has no colon, so this would otherwise fail to parse and
        // silently drop the issue from every time-based rollup.
        assert_eq!(
            to_rfc3339("2026-06-15T09:00:00.000+0000"),
            "2026-06-15T09:00:00.000+00:00"
        );
        assert_eq!(
            to_rfc3339("2026-06-15T09:00:00.000-0530"),
            "2026-06-15T09:00:00.000-05:30"
        );
        // Already-valid values and Z-suffixed values pass through untouched.
        assert_eq!(to_rfc3339("2026-06-15T09:00:00Z"), "2026-06-15T09:00:00Z");
        assert_eq!(
            to_rfc3339("2026-06-15T09:00:00+00:00"),
            "2026-06-15T09:00:00+00:00"
        );
        assert_eq!(to_rfc3339(""), "");
    }

    #[test]
    fn parses_a_realistic_search_page() {
        let raw = serde_json::json!({
            "issues": [{
                "key": "ENG-412",
                "fields": {
                    "summary": "Search returns stale results",
                    "description": {
                        "type": "doc",
                        "content": [{ "type": "paragraph", "content": [
                            { "type": "text", "text": "Cache is not invalidated." }
                        ]}]
                    },
                    "status": { "statusCategory": { "key": "indeterminate" } },
                    "labels": ["bug", "search"],
                    "created": "2026-05-01T09:00:00.000+0000",
                    "updated": "2026-06-01T09:00:00.000+0000",
                    "resolutiondate": null,
                    "assignee": { "displayName": "Ada", "emailAddress": "ada@example.com" }
                }
            }],
            "nextPageToken": "abc"
        });
        let page: SearchPage = serde_json::from_value(raw).unwrap();
        assert_eq!(page.issues.len(), 1);
        assert_eq!(page.next_page_token.as_deref(), Some("abc"));

        let f = &page.issues[0].fields;
        assert_eq!(f.summary, "Search returns stale results");
        assert_eq!(adf_to_text(&f.description), "Cache is not invalidated.");
        assert_eq!(f.labels, vec!["bug".to_string(), "search".to_string()]);
        let cat = f.status.as_ref().unwrap().status_category.as_ref().unwrap();
        assert_eq!(
            state_from_jira_category(&cat.key),
            gitstate_core::WorkState::InProgress
        );
    }

    #[test]
    fn tolerates_a_sparse_issue() {
        // Jira omits fields the token can't see; none of that should panic.
        let page: SearchPage =
            serde_json::from_value(serde_json::json!({ "issues": [{ "key": "X-1" }] })).unwrap();
        let f = &page.issues[0].fields;
        assert_eq!(f.summary, "");
        assert!(f.status.is_none());
        assert!(f.labels.is_empty());
        assert_eq!(adf_to_text(&f.description), "");
    }
}
