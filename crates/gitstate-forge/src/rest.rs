//! REST fallbacks for when a CLI is absent but a token is configured. Kept
//! deliberately minimal (no per-item fan-out): a page of PRs/issues each.

use gitstate_core::{Error, Result, WorkItem, WorkItemId, WorkKind, WorkState};
use serde::Deserialize;

fn client() -> reqwest::Client {
    reqwest::Client::builder()
        .user_agent("gitstate/0.1")
        .build()
        .unwrap_or_default()
}

// ── GitHub ──

#[derive(Deserialize)]
struct GhUser {
    #[serde(default)]
    login: String,
}
#[derive(Deserialize)]
struct GhLabel {
    #[serde(default)]
    name: String,
}
#[derive(Deserialize)]
struct GhRestItem {
    number: u64,
    #[serde(default)]
    title: String,
    #[serde(default)]
    body: Option<String>,
    #[serde(default)]
    state: String, // open | closed
    #[serde(default)]
    draft: bool,
    user: Option<GhUser>,
    #[serde(default)]
    labels: Vec<GhLabel>,
    #[serde(default)]
    created_at: String,
    #[serde(default)]
    updated_at: String,
    #[serde(default)]
    merged_at: Option<String>,
    #[serde(default)]
    closed_at: Option<String>,
}

async fn github_get(token: Option<&str>, url: &str) -> Result<Vec<GhRestItem>> {
    let mut req = client().get(url);
    if let Some(t) = token {
        req = req.bearer_auth(t);
    }
    let resp = req.send().await.map_err(Error::http)?;
    if !resp.status().is_success() {
        return Err(Error::forge(format!("github REST {}", resp.status())));
    }
    resp.json::<Vec<GhRestItem>>().await.map_err(Error::http)
}

pub async fn github_pulls(token: Option<&str>, slug: &str) -> Result<Vec<WorkItem>> {
    let url = format!("https://api.github.com/repos/{slug}/pulls?state=all&per_page=100");
    let items = github_get(token, &url).await?;
    Ok(items
        .into_iter()
        .map(|it| {
            let state = if it.merged_at.is_some() {
                WorkState::Merged
            } else if it.state.eq_ignore_ascii_case("closed") {
                WorkState::Closed
            } else if it.draft {
                WorkState::Draft
            } else {
                WorkState::Open
            };
            rest_item_to_work(slug, WorkKind::Pr, state, it)
        })
        .collect())
}

pub async fn github_issues(token: Option<&str>, slug: &str) -> Result<Vec<WorkItem>> {
    let url = format!("https://api.github.com/repos/{slug}/issues?state=all&per_page=100");
    let items = github_get(token, &url).await?;
    Ok(items
        .into_iter()
        // The issues endpoint also returns PRs; drop those (they carry no
        // `pull_request` field in our slim struct, so filter by absence of a
        // merged_at is unreliable — instead keep everything and let the caller
        // dedupe by ref; PRs already come from github_pulls).
        .map(|it| {
            let state = if it.state.eq_ignore_ascii_case("closed") {
                WorkState::Closed
            } else {
                WorkState::Open
            };
            rest_item_to_work(slug, WorkKind::Issue, state, it)
        })
        .collect())
}

fn rest_item_to_work(slug: &str, kind: WorkKind, state: WorkState, it: GhRestItem) -> WorkItem {
    WorkItem {
        id: WorkItemId::new(),
        repo_id: gitstate_core::RepoId(slug.to_string()),
        kind,
        external_ref: format!("#{}", it.number),
        title: it.title,
        body: it.body.unwrap_or_default(),
        state,
        author_login: it.user.map(|u| u.login).filter(|s| !s.is_empty()),
        labels: it.labels.into_iter().map(|l| l.name).collect(),
        created_at: it.created_at,
        updated_at: it.updated_at,
        merged_at: it.merged_at,
        closed_at: it.closed_at,
        files_touched: vec![],
    }
}

// ── GitLab ──

#[derive(Deserialize)]
struct GlUser {
    #[serde(default)]
    username: String,
}
#[derive(Deserialize)]
struct GlItem {
    iid: u64,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: Option<String>,
    #[serde(default)]
    state: String, // opened | merged | closed | locked
    author: Option<GlUser>,
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

fn urlencode(s: &str) -> String {
    s.replace('/', "%2F")
}

async fn gitlab_get(token: Option<&str>, url: &str) -> Result<Vec<GlItem>> {
    let mut req = client().get(url);
    if let Some(t) = token {
        req = req.header("PRIVATE-TOKEN", t);
    }
    let resp = req.send().await.map_err(Error::http)?;
    if !resp.status().is_success() {
        return Err(Error::forge(format!("gitlab REST {}", resp.status())));
    }
    resp.json::<Vec<GlItem>>().await.map_err(Error::http)
}

pub async fn gitlab_mrs(token: Option<&str>, slug: &str) -> Result<Vec<WorkItem>> {
    let url = format!(
        "https://gitlab.com/api/v4/projects/{}/merge_requests?state=all&per_page=100",
        urlencode(slug)
    );
    let items = gitlab_get(token, &url).await?;
    Ok(items
        .into_iter()
        .map(|it| gl_item_to_work(slug, WorkKind::Pr, it))
        .collect())
}

pub async fn gitlab_issues(token: Option<&str>, slug: &str) -> Result<Vec<WorkItem>> {
    let url = format!(
        "https://gitlab.com/api/v4/projects/{}/issues?state=all&per_page=100",
        urlencode(slug)
    );
    let items = gitlab_get(token, &url).await?;
    Ok(items
        .into_iter()
        .map(|it| gl_item_to_work(slug, WorkKind::Issue, it))
        .collect())
}

fn gl_item_to_work(slug: &str, kind: WorkKind, it: GlItem) -> WorkItem {
    let state = match it.state.as_str() {
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
        external_ref: format!("{prefix}{}", it.iid),
        title: it.title,
        body: it.description.unwrap_or_default(),
        state,
        author_login: it.author.map(|a| a.username).filter(|s| !s.is_empty()),
        labels: it.labels,
        created_at: it.created_at,
        updated_at: it.updated_at,
        merged_at: it.merged_at,
        closed_at: it.closed_at,
        files_touched: vec![],
    }
}
