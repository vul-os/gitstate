//! GitHub forge client: shells `gh --json`, falling back to the REST API when
//! the CLI is absent but a token is set. No network happens for a `Local` repo
//! scan — this client is only consulted when the user asks for forge data.

use crate::cli::{cli_available, run_cli, slug_from_remote};
use crate::rest;
use async_trait::async_trait;
use gitstate_core::{Error, Forge, ForgeClient, Result, WorkItem, WorkItemId, WorkKind, WorkState};
use serde::Deserialize;

const PAGE_LIMIT: &str = "200";

/// GitHub client. `token` enables the REST fallback; `gh_bin` is the CLI name.
pub struct GitHubClient {
    pub(crate) token: Option<String>,
    pub(crate) gh_bin: String,
}

impl GitHubClient {
    /// Build from the environment. Token from
    /// `GITSTATE_GH_TOKEN` | `GH_TOKEN` | `GITHUB_TOKEN`.
    pub fn from_env() -> Self {
        let token = std::env::var("GITSTATE_GH_TOKEN")
            .ok()
            .or_else(|| std::env::var("GH_TOKEN").ok())
            .or_else(|| std::env::var("GITHUB_TOKEN").ok())
            .filter(|s| !s.is_empty());
        GitHubClient {
            token,
            gh_bin: "gh".to_string(),
        }
    }

    async fn use_cli(&self) -> bool {
        cli_available(&self.gh_bin).await
    }
}

#[async_trait]
impl ForgeClient for GitHubClient {
    fn forge(&self) -> Forge {
        Forge::GitHub
    }

    async fn list_pull_requests(&self, slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        if self.use_cli().await {
            let out = run_cli(
                &self.gh_bin,
                &[
                    "pr", "list", "--repo", slug, "--state", "all", "--limit", PAGE_LIMIT,
                    "--json",
                    "number,title,body,state,isDraft,author,labels,createdAt,updatedAt,mergedAt,closedAt,files",
                ],
            )
            .await?;
            let prs: Vec<GhPr> = serde_json::from_str(&out)?;
            Ok(prs.into_iter().map(|p| p.into_work_item(slug)).collect())
        } else if self.token.is_some() {
            rest::github_pulls(self.token.as_deref(), slug).await
        } else {
            Err(Error::ForgeCliMissing("gh".into()))
        }
    }

    async fn list_issues(&self, slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        if self.use_cli().await {
            let out = run_cli(
                &self.gh_bin,
                &[
                    "issue",
                    "list",
                    "--repo",
                    slug,
                    "--state",
                    "all",
                    "--limit",
                    PAGE_LIMIT,
                    "--json",
                    "number,title,body,state,author,labels,createdAt,updatedAt,closedAt",
                ],
            )
            .await?;
            let issues: Vec<GhIssue> = serde_json::from_str(&out)?;
            Ok(issues.into_iter().map(|i| i.into_work_item(slug)).collect())
        } else if self.token.is_some() {
            rest::github_issues(self.token.as_deref(), slug).await
        } else {
            Err(Error::ForgeCliMissing("gh".into()))
        }
    }

    async fn list_reviews(&self, slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        // Reviews are attached to PRs; flatten one WorkItem per review.
        if !self.use_cli().await {
            // REST review enumeration would be N+1; skip unless CLI present.
            return Ok(vec![]);
        }
        let out = run_cli(
            &self.gh_bin,
            &[
                "pr",
                "list",
                "--repo",
                slug,
                "--state",
                "all",
                "--limit",
                PAGE_LIMIT,
                "--json",
                "number,reviews",
            ],
        )
        .await?;
        let prs: Vec<GhReviewsOf> = serde_json::from_str(&out)?;
        let mut out_items = Vec::new();
        for pr in prs {
            for (i, r) in pr.reviews.into_iter().enumerate() {
                let login = r.author.map(|a| a.login).unwrap_or_default();
                if login.is_empty() {
                    continue;
                }
                out_items.push(WorkItem {
                    id: WorkItemId::new(),
                    repo_id: gitstate_core::RepoId(slug.to_string()),
                    kind: WorkKind::Review,
                    external_ref: format!("#{}-review-{}", pr.number, i + 1),
                    title: format!("review on #{}", pr.number),
                    body: r.body.unwrap_or_default(),
                    state: WorkState::Done,
                    author_login: Some(login),
                    labels: vec![],
                    created_at: r.submitted_at.clone().unwrap_or_default(),
                    updated_at: r.submitted_at.unwrap_or_default(),
                    merged_at: None,
                    closed_at: None,
                    files_touched: vec![],
                });
            }
        }
        Ok(out_items)
    }

    async fn resolve_slug(&self, remote_url: &str) -> Result<String> {
        slug_from_remote(remote_url)
    }
}

// ── gh --json wire types ──

#[derive(Deserialize)]
struct GhAuthor {
    #[serde(default)]
    login: String,
}
#[derive(Deserialize)]
struct GhLabel {
    #[serde(default)]
    name: String,
}
#[derive(Deserialize)]
struct GhFile {
    #[serde(default)]
    path: String,
}

#[derive(Deserialize)]
struct GhPr {
    number: u64,
    #[serde(default)]
    title: String,
    #[serde(default)]
    body: String,
    #[serde(default)]
    state: String, // OPEN | CLOSED | MERGED
    #[serde(default, rename = "isDraft")]
    is_draft: bool,
    author: Option<GhAuthor>,
    #[serde(default)]
    labels: Vec<GhLabel>,
    #[serde(default, rename = "createdAt")]
    created_at: String,
    #[serde(default, rename = "updatedAt")]
    updated_at: String,
    #[serde(default, rename = "mergedAt")]
    merged_at: Option<String>,
    #[serde(default, rename = "closedAt")]
    closed_at: Option<String>,
    #[serde(default)]
    files: Vec<GhFile>,
}

impl GhPr {
    fn into_work_item(self, slug: &str) -> WorkItem {
        let state = match self.state.to_uppercase().as_str() {
            "MERGED" => WorkState::Merged,
            "CLOSED" => WorkState::Closed,
            _ if self.is_draft => WorkState::Draft,
            _ => WorkState::Open,
        };
        WorkItem {
            id: WorkItemId::new(),
            repo_id: gitstate_core::RepoId(slug.to_string()),
            kind: WorkKind::Pr,
            external_ref: format!("#{}", self.number),
            title: self.title,
            body: self.body,
            state,
            author_login: self.author.map(|a| a.login).filter(|s| !s.is_empty()),
            labels: self.labels.into_iter().map(|l| l.name).collect(),
            created_at: self.created_at,
            updated_at: self.updated_at,
            merged_at: self.merged_at,
            closed_at: self.closed_at,
            files_touched: self.files.into_iter().map(|f| f.path).collect(),
        }
    }
}

#[derive(Deserialize)]
struct GhIssue {
    number: u64,
    #[serde(default)]
    title: String,
    #[serde(default)]
    body: String,
    #[serde(default)]
    state: String, // OPEN | CLOSED
    author: Option<GhAuthor>,
    #[serde(default)]
    labels: Vec<GhLabel>,
    #[serde(default, rename = "createdAt")]
    created_at: String,
    #[serde(default, rename = "updatedAt")]
    updated_at: String,
    #[serde(default, rename = "closedAt")]
    closed_at: Option<String>,
}

impl GhIssue {
    fn into_work_item(self, slug: &str) -> WorkItem {
        let state = match self.state.to_uppercase().as_str() {
            "CLOSED" => WorkState::Closed,
            _ => WorkState::Open,
        };
        WorkItem {
            id: WorkItemId::new(),
            repo_id: gitstate_core::RepoId(slug.to_string()),
            kind: WorkKind::Issue,
            external_ref: format!("#{}", self.number),
            title: self.title,
            body: self.body,
            state,
            author_login: self.author.map(|a| a.login).filter(|s| !s.is_empty()),
            labels: self.labels.into_iter().map(|l| l.name).collect(),
            created_at: self.created_at,
            updated_at: self.updated_at,
            merged_at: None,
            closed_at: self.closed_at,
            files_touched: vec![],
        }
    }
}

#[derive(Deserialize)]
struct GhReviewsOf {
    number: u64,
    #[serde(default)]
    reviews: Vec<GhReview>,
}
#[derive(Deserialize)]
struct GhReview {
    author: Option<GhAuthor>,
    #[serde(default)]
    body: Option<String>,
    #[serde(default, rename = "submittedAt")]
    submitted_at: Option<String>,
}
