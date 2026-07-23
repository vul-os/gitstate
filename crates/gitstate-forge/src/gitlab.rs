//! GitLab forge client: shells `glab … -F json`, falling back to the REST v4
//! API when the CLI is absent but a token is set. Merge-request reviews map to
//! GitLab approvals, which are not enumerated here (returned empty).

use crate::cli::{cli_available, run_cli, slug_from_remote};
use crate::rest;
use async_trait::async_trait;
use gitstate_core::{Error, Forge, ForgeClient, Result, WorkItem, WorkItemId, WorkKind, WorkState};
use serde::Deserialize;

const PER_PAGE: &str = "100";

/// GitLab client. `token` enables the REST fallback; `glab_bin` is the CLI name.
pub struct GitLabClient {
    pub(crate) token: Option<String>,
    pub(crate) glab_bin: String,
}

impl GitLabClient {
    /// Build from the environment. Token from
    /// `GITSTATE_GLAB_TOKEN` | `GITLAB_TOKEN`.
    pub fn from_env() -> Self {
        let token = std::env::var("GITSTATE_GLAB_TOKEN")
            .ok()
            .or_else(|| std::env::var("GITLAB_TOKEN").ok())
            .filter(|s| !s.is_empty());
        GitLabClient {
            token,
            glab_bin: "glab".to_string(),
        }
    }

    async fn use_cli(&self) -> bool {
        cli_available(&self.glab_bin).await
    }
}

#[async_trait]
impl ForgeClient for GitLabClient {
    fn forge(&self) -> Forge {
        Forge::GitLab
    }

    async fn list_pull_requests(&self, slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        if self.use_cli().await {
            let out = run_cli(
                &self.glab_bin,
                &[
                    "mr", "list", "-R", slug, "--all", "-P", PER_PAGE, "-F", "json",
                ],
            )
            .await?;
            let items: Vec<GlMr> = serde_json::from_str(&out)?;
            Ok(items
                .into_iter()
                .map(|m| m.into_work_item(slug, WorkKind::Pr))
                .collect())
        } else if self.token.is_some() {
            rest::gitlab_mrs(self.token.as_deref(), slug).await
        } else {
            Err(Error::ForgeCliMissing("glab".into()))
        }
    }

    async fn list_issues(&self, slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        if self.use_cli().await {
            let out = run_cli(
                &self.glab_bin,
                &[
                    "issue", "list", "-R", slug, "--all", "-P", PER_PAGE, "-F", "json",
                ],
            )
            .await?;
            let items: Vec<GlMr> = serde_json::from_str(&out)?;
            Ok(items
                .into_iter()
                .map(|m| m.into_work_item(slug, WorkKind::Issue))
                .collect())
        } else if self.token.is_some() {
            rest::gitlab_issues(self.token.as_deref(), slug).await
        } else {
            Err(Error::ForgeCliMissing("glab".into()))
        }
    }

    async fn list_reviews(&self, _slug: &str, _since: Option<&str>) -> Result<Vec<WorkItem>> {
        // GitLab approvals ≠ line reviews; not enumerated in v1.
        Ok(vec![])
    }

    async fn resolve_slug(&self, remote_url: &str) -> Result<String> {
        slug_from_remote(remote_url)
    }
}

// ── glab -F json / REST wire type (same object shape) ──

#[derive(Deserialize)]
struct GlAuthor {
    #[serde(default)]
    username: String,
}
#[derive(Deserialize)]
struct GlMr {
    iid: u64,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: Option<String>,
    #[serde(default)]
    state: String,
    author: Option<GlAuthor>,
    #[serde(default)]
    labels: Vec<String>,
    #[serde(default)]
    created_at: String,
    #[serde(default)]
    updated_at: String,
    #[serde(default)]
    merged_at: Option<String>,
    #[serde(default)]
    closed_at: Option<String>,
}

impl GlMr {
    fn into_work_item(self, slug: &str, kind: WorkKind) -> WorkItem {
        let state = match self.state.as_str() {
            "merged" => WorkState::Merged,
            "closed" | "locked" => WorkState::Closed,
            _ => WorkState::Open,
        };
        let prefix = if matches!(kind, WorkKind::Pr) {
            "!"
        } else {
            "#"
        };
        WorkItem {
            id: WorkItemId::new(),
            repo_id: gitstate_core::RepoId(slug.to_string()),
            kind,
            external_ref: format!("{prefix}{}", self.iid),
            title: self.title,
            body: self.description.unwrap_or_default(),
            state,
            author_login: self.author.map(|a| a.username).filter(|s| !s.is_empty()),
            labels: self.labels,
            created_at: self.created_at,
            updated_at: self.updated_at,
            merged_at: self.merged_at,
            closed_at: self.closed_at,
            files_touched: vec![],
        }
    }
}
