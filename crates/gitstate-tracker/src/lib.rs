//! gitstate-tracker — the [`TrackerClient`] seam for Jira and Linear.
//!
//! **Local-first, like everything else.** Both vendors issue *personal* API
//! tokens, so the daemon calls their public HTTPS API **from your machine with
//! your own credential** — exactly the posture the `gh`/`glab` forge clients
//! already take. There is no broker, no OAuth callback, and no gitstate server
//! in the path. The token lives in your local SQLite file and is never sent
//! anywhere except to the vendor it belongs to.
//!
//! For an air-gapped machine (or a Jira Server/DC instance that won't issue a
//! token), [`from_export`] parses the JSON/CSV files both products can export,
//! so an import never *requires* network access at all.
//!
//! Everything lands as [`WorkItem`]s in the same shape the forge clients
//! produce, so classification, effort judging and every analytics rollup treat
//! imported issues identically to native ones.

mod export;
mod jira;
mod linear;
mod map;

pub use export::from_export;
pub use jira::JiraClient;
pub use linear::LinearClient;
pub use map::{stable_item_id, ImportedItem};

use async_trait::async_trait;
use gitstate_core::{Error, RepoId, Result, WorkItem};
use serde::{Deserialize, Serialize};

/// Which tracker a credential/import belongs to.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum TrackerKind {
    Jira,
    Linear,
}

impl TrackerKind {
    pub fn as_str(&self) -> &'static str {
        match self {
            TrackerKind::Jira => "jira",
            TrackerKind::Linear => "linear",
        }
    }
    pub fn parse(s: &str) -> Result<Self> {
        match s.to_ascii_lowercase().as_str() {
            "jira" => Ok(TrackerKind::Jira),
            "linear" => Ok(TrackerKind::Linear),
            other => Err(Error::invalid(format!("unknown tracker: {other}"))),
        }
    }
}

/// A tracker credential, as stored locally.
///
/// `token` is write-only over the HTTP API: it is accepted on save and used on
/// import, but reads return [`TrackerConfig::redacted`] instead.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct TrackerConfig {
    /// Jira only: `https://your-site.atlassian.net`. Ignored by Linear.
    #[serde(default)]
    pub base_url: String,
    /// Jira only: the Atlassian account email paired with the API token.
    #[serde(default)]
    pub email: String,
    /// Jira API token, or Linear personal API key.
    #[serde(default)]
    pub token: String,
    /// Optional scope: a Jira project key (`ENG`) or a Linear team key (`ENG`).
    #[serde(default)]
    pub project: String,
}

impl TrackerConfig {
    /// A copy safe to return over the API: the token becomes a masked hint so
    /// the UI can show *that* a credential exists without ever re-exposing it.
    pub fn redacted(&self) -> TrackerConfig {
        TrackerConfig {
            base_url: self.base_url.clone(),
            email: self.email.clone(),
            token: mask(&self.token),
            project: self.project.clone(),
        }
    }

    pub fn is_configured(&self) -> bool {
        !self.token.is_empty()
    }
}

/// `"abcd1234efgh"` → `"…efgh"`. Empty stays empty so the UI can distinguish
/// "not configured" from "configured".
fn mask(token: &str) -> String {
    if token.is_empty() {
        return String::new();
    }
    let tail: String = token
        .chars()
        .rev()
        .take(4)
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .collect();
    format!("…{tail}")
}

/// The outcome of verifying a credential, for the UI's "Test connection".
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TrackerStatus {
    pub ok: bool,
    /// Who the token authenticates as, when the vendor tells us.
    pub account: Option<String>,
    pub message: String,
}

/// A tracker as seen through its public API with a personal token.
#[async_trait]
pub trait TrackerClient: Send + Sync {
    fn kind(&self) -> TrackerKind;
    /// Verify the credential without importing anything.
    async fn test(&self) -> Result<TrackerStatus>;
    /// Fetch issues, newest first. `limit` caps the result so a preview is cheap.
    async fn fetch(&self, limit: usize) -> Result<Vec<ImportedItem>>;
}

/// Build the right client for a stored credential.
pub fn client_for(kind: TrackerKind, cfg: &TrackerConfig) -> Result<Box<dyn TrackerClient>> {
    if !cfg.is_configured() {
        return Err(Error::invalid(format!(
            "{} is not configured: add an API token first",
            kind.as_str()
        )));
    }
    match kind {
        TrackerKind::Jira => Ok(Box::new(JiraClient::new(cfg)?)),
        TrackerKind::Linear => Ok(Box::new(LinearClient::new(cfg))),
    }
}

/// Attach imported items to a repo, producing storable [`WorkItem`]s.
pub fn to_work_items(items: &[ImportedItem], repo: &RepoId) -> Vec<WorkItem> {
    items.iter().map(|i| i.to_work_item(repo)).collect()
}

/// Shared HTTP client. A short timeout keeps a wedged tracker from hanging the
/// daemon's import request forever.
pub(crate) fn http() -> reqwest::Client {
    reqwest::Client::builder()
        .user_agent(concat!("gitstate/", env!("CARGO_PKG_VERSION")))
        .timeout(std::time::Duration::from_secs(30))
        .build()
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn kind_roundtrips() {
        assert_eq!(TrackerKind::parse("jira").unwrap(), TrackerKind::Jira);
        assert_eq!(TrackerKind::parse("LINEAR").unwrap(), TrackerKind::Linear);
        assert!(TrackerKind::parse("asana").is_err());
        assert_eq!(TrackerKind::Jira.as_str(), "jira");
    }

    #[test]
    fn redaction_never_leaks_the_token() {
        let cfg = TrackerConfig {
            token: "super-secret-token-9999".into(),
            email: "dev@example.com".into(),
            ..Default::default()
        };
        let r = cfg.redacted();
        assert_eq!(r.token, "…9999");
        assert!(!r.token.contains("secret"));
        // Non-secret fields still round-trip so the UI can show the account.
        assert_eq!(r.email, "dev@example.com");
    }

    #[test]
    fn redaction_distinguishes_unset_from_set() {
        assert_eq!(TrackerConfig::default().redacted().token, "");
        assert!(!TrackerConfig::default().is_configured());
    }

    #[test]
    fn client_for_refuses_an_unconfigured_tracker() {
        let err = client_for(TrackerKind::Jira, &TrackerConfig::default())
            .err()
            .expect("an unconfigured tracker must not build a client");
        assert!(err.to_string().contains("not configured"), "{err}");
    }
}
