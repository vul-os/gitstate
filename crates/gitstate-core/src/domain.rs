//! The gitstate domain model: the true project state, effort, contribution
//! (six gaming-resistant dimensions), classification, and the sharable
//! Context. Pure data — no I/O, no derivation logic (that lives in
//! [`crate::derive`]).

use crate::ids::*;
use serde::{Deserialize, Serialize};

/// Where a repository's forge lives (or `Local` for a bare worktree).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Forge {
    GitHub,
    GitLab,
    Local,
}

impl Forge {
    pub fn as_str(&self) -> &'static str {
        match self {
            Forge::GitHub => "github",
            Forge::GitLab => "gitlab",
            Forge::Local => "local",
        }
    }
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "github" => Ok(Forge::GitHub),
            "gitlab" => Ok(Forge::GitLab),
            "local" => Ok(Forge::Local),
            other => Err(crate::Error::invalid(format!("unknown forge: {other}"))),
        }
    }
}

/// A registered repository (a local worktree, optionally forge-backed).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Repo {
    pub id: RepoId,
    /// "owner/name" for a forge repo, or the directory name for `Local`.
    pub slug: String,
    /// Absolute local worktree path.
    pub path: String,
    pub remote_url: Option<String>,
    pub forge: Forge,
    pub default_branch: String,
    pub last_scanned_at: Option<String>,
    pub added_at: String,
}

/// A merged contributor identity (one human or agent, many emails/logins).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Contributor {
    pub id: ContributorId,
    pub display_name: String,
    pub primary_email: String,
    /// Alias emails clustered into this identity.
    pub emails: Vec<String>,
    /// Forge handle, if known.
    pub login: Option<String>,
    /// Agent/bot identity (agent-native).
    pub is_agent: bool,
    /// e.g. "claude-code", "dependabot".
    pub agent_kind: Option<String>,
}

/// The four kinds of forge/git unit.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum WorkKind {
    Commit,
    Pr,
    Issue,
    Review,
}

impl WorkKind {
    pub fn as_str(&self) -> &'static str {
        match self {
            WorkKind::Commit => "commit",
            WorkKind::Pr => "pr",
            WorkKind::Issue => "issue",
            WorkKind::Review => "review",
        }
    }
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "commit" => Ok(WorkKind::Commit),
            "pr" => Ok(WorkKind::Pr),
            "issue" => Ok(WorkKind::Issue),
            "review" => Ok(WorkKind::Review),
            other => Err(crate::Error::invalid(format!("unknown work kind: {other}"))),
        }
    }
}

/// Lifecycle state of a work item.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum WorkState {
    Open,
    InProgress,
    Merged,
    Closed,
    Done,
    Draft,
}

impl WorkState {
    pub fn as_str(&self) -> &'static str {
        match self {
            WorkState::Open => "open",
            WorkState::InProgress => "in_progress",
            WorkState::Merged => "merged",
            WorkState::Closed => "closed",
            WorkState::Done => "done",
            WorkState::Draft => "draft",
        }
    }
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "open" => Ok(WorkState::Open),
            "in_progress" => Ok(WorkState::InProgress),
            "merged" => Ok(WorkState::Merged),
            "closed" => Ok(WorkState::Closed),
            "done" => Ok(WorkState::Done),
            "draft" => Ok(WorkState::Draft),
            other => Err(crate::Error::invalid(format!(
                "unknown work state: {other}"
            ))),
        }
    }
}

/// The unit fed to the [`crate::traits::Classifier`] — a PR, issue, commit, or
/// review with its metadata (never any raw source).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkItem {
    pub id: WorkItemId,
    pub repo_id: RepoId,
    pub kind: WorkKind,
    /// "#123", a sha, or "!45".
    pub external_ref: String,
    pub title: String,
    pub body: String,
    pub state: WorkState,
    pub author_login: Option<String>,
    pub labels: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    pub merged_at: Option<String>,
    pub closed_at: Option<String>,
    pub files_touched: Vec<String>,
}

/// A single commit's derived aggregates. No source is ever stored — only the
/// shape (counts) and the first summary line.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Commit {
    pub sha: String,
    pub repo_id: RepoId,
    pub author_email: String,
    pub author_name: String,
    pub committed_at: String,
    pub additions: u32,
    pub deletions: u32,
    pub files_changed: u32,
    pub is_merge: bool,
    /// Touched at least one test path (test-coupling signal).
    pub is_test_touch: bool,
    /// First line of the commit message only.
    pub summary: String,
}

/// Lines still present at HEAD, per author, from `git blame`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuthorSurvival {
    pub author_email: String,
    pub surviving_lines: u32,
    pub authored_lines: u32,
}

/// A bug-introducing → bug-fixing edge from the SZZ heuristic.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BugIntroduction {
    pub author_email: String,
    pub introduced_sha: String,
    pub fix_sha: String,
    pub lines: u32,
}

/// The derived, whole-repo state (DORA-flavoured).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectState {
    pub repo_id: RepoId,
    pub head_sha: String,
    pub open_prs: u32,
    pub merged_prs: u32,
    pub draft_prs: u32,
    pub open_issues: u32,
    pub closed_issues: u32,
    /// Open PR ⇒ in progress.
    pub in_progress: u32,
    /// Merged PR / closed issue ⇒ done.
    pub done: u32,
    /// First-commit→merge (DORA lead time).
    pub cycle_time_p50_hours: Option<f64>,
    pub cycle_time_p90_hours: Option<f64>,
    pub change_failure_rate: Option<f64>,
    pub computed_at: String,
    pub warnings: Vec<String>,
}

/// The six dimensions — the gitstate essence. Each normalized 0–100 within the
/// repo cohort; never a leaderboard by itself.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
pub struct Dimensions {
    pub shipped: f64,
    pub review: f64,
    pub effort: f64,
    pub quality: f64,
    pub ownership: f64,
    pub durability: f64,
}

/// Pre-normalization evidence behind [`Dimensions`].
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
pub struct DimensionRaw {
    pub merged_prs: u32,
    pub closed_issues: u32,
    pub reviews_done: u32,
    /// Σ judged diff-difficulty.
    pub effort_points: f64,
    pub reverts_caused: u32,
    pub bug_intros: u32,
    pub areas_owned: u32,
    pub surviving_lines: u32,
    pub authored_lines: u32,
    pub human_commits: u32,
    pub agent_commits: u32,
}

impl Default for DimensionRaw {
    fn default() -> Self {
        DimensionRaw {
            merged_prs: 0,
            closed_issues: 0,
            reviews_done: 0,
            effort_points: 0.0,
            reverts_caused: 0,
            bug_intros: 0,
            areas_owned: 0,
            surviving_lines: 0,
            authored_lines: 0,
            human_commits: 0,
            agent_commits: 0,
        }
    }
}

/// One contributor's derived contribution across a window.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Contribution {
    pub contributor_id: ContributorId,
    pub repo_id: RepoId,
    /// Window bounds (RFC3339).
    pub from: String,
    pub to: String,
    pub dimensions: Dimensions,
    pub raw: DimensionRaw,
    /// 0..1 share of commits from agent identities.
    pub agent_pct: f64,
    /// Weighted 0–100; DISPLAY as texture, never a rank.
    pub composite: f64,
}

/// Per-dimension weights for the composite. Sum is normalized to 1.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
pub struct Weights {
    pub shipped: f64,
    pub review: f64,
    pub effort: f64,
    pub quality: f64,
    pub ownership: f64,
    pub durability: f64,
}

impl Weights {
    pub fn default_weights() -> Self {
        Weights {
            shipped: 1.0,
            review: 1.0,
            effort: 1.0,
            quality: 1.0,
            ownership: 1.0,
            durability: 1.0,
        }
    }

    /// Normalize so the six weights sum to 1. An all-zero set becomes 1/6 each.
    pub fn normalized(&self) -> Self {
        let sum = self.shipped
            + self.review
            + self.effort
            + self.quality
            + self.ownership
            + self.durability;
        if sum <= f64::EPSILON {
            let s = 1.0 / 6.0;
            return Weights {
                shipped: s,
                review: s,
                effort: s,
                quality: s,
                ownership: s,
                durability: s,
            };
        }
        Weights {
            shipped: self.shipped / sum,
            review: self.review / sum,
            effort: self.effort / sum,
            quality: self.quality / sum,
            ownership: self.ownership / sum,
            durability: self.durability / sum,
        }
    }
}

impl Default for Weights {
    fn default() -> Self {
        Weights::default_weights()
    }
}

/// How an effort/classification judgement was produced.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EffortMethod {
    LlmJudged,
    Heuristic,
}

impl EffortMethod {
    pub fn as_str(&self) -> &'static str {
        match self {
            EffortMethod::LlmJudged => "llm_judged",
            EffortMethod::Heuristic => "heuristic",
        }
    }
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "llm_judged" => Ok(EffortMethod::LlmJudged),
            "heuristic" => Ok(EffortMethod::Heuristic),
            other => Err(crate::Error::invalid(format!("unknown method: {other}"))),
        }
    }
}

/// Input to effort judging: the *shape* of a diff, never its source.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DiffSummary {
    pub item_id: WorkItemId,
    pub external_ref: String,
    pub additions: u32,
    pub deletions: u32,
    pub files: u32,
    pub languages: Vec<String>,
    pub touched_paths: Vec<String>,
    pub title: String,
    pub body: String,
}

/// A judged difficulty for one work item.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EffortEstimate {
    pub item_id: WorkItemId,
    /// 1.0..=13.0 (fibonacci-ish), NOT a line count.
    pub difficulty: f64,
    pub method: EffortMethod,
    pub rationale: String,
    pub confidence: f64,
}

/// Where a category came from.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum CategorySource {
    Taxonomy,
    Local,
    Peer,
}

impl CategorySource {
    pub fn as_str(&self) -> &'static str {
        match self {
            CategorySource::Taxonomy => "taxonomy",
            CategorySource::Local => "local",
            CategorySource::Peer => "peer",
        }
    }
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "taxonomy" => Ok(CategorySource::Taxonomy),
            "local" => Ok(CategorySource::Local),
            "peer" => Ok(CategorySource::Peer),
            other => Err(crate::Error::invalid(format!("unknown source: {other}"))),
        }
    }
}

/// A CRDT-backed category (label alignment aid).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Category {
    pub id: CategoryId,
    /// Stable dotted key, e.g. "feature.api".
    pub key: String,
    pub label: String,
    pub parent_key: Option<String>,
    /// "#rrggbb".
    pub color: Option<String>,
    pub source: CategorySource,
    /// Set when `source == Taxonomy`.
    pub taxonomy_version: Option<String>,
    pub hlc: Hlc,
    /// Tombstone.
    pub deleted: bool,
}

/// A classifier's category assignment for a work item.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Classification {
    pub item_id: WorkItemId,
    pub category_key: String,
    pub confidence: f64,
    pub method: EffortMethod,
    pub rationale: String,
}

/// A referenced PR inside a [`Context`].
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ContextPrRef {
    pub repo_slug: String,
    pub number: u64,
    pub note: Option<String>,
}

/// A saved working set — the sharable, CRDT-backed unit.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Context {
    pub id: ContextId,
    pub name: String,
    pub description: String,
    /// OR-Set.
    pub repo_ids: Vec<RepoId>,
    /// OR-Set.
    pub pr_refs: Vec<ContextPrRef>,
    /// LWW text.
    pub notes: String,
    /// OR-Set.
    pub tags: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    pub hlc: Hlc,
    pub deleted: bool,
}

// ── CRDT op envelope (defined here so store + sync agree) — see §5. ──

/// LWW-scalar fields of a [`Context`].
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CtxField {
    Name,
    Description,
    Notes,
}

impl CtxField {
    pub fn as_str(&self) -> &'static str {
        match self {
            CtxField::Name => "name",
            CtxField::Description => "description",
            CtxField::Notes => "notes",
        }
    }
}

/// LWW-scalar fields of a [`Category`].
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CatField {
    Label,
    Color,
    ParentKey,
}

impl CatField {
    pub fn as_str(&self) -> &'static str {
        match self {
            CatField::Label => "label",
            CatField::Color => "color",
            CatField::ParentKey => "parent_key",
        }
    }
}

/// The transport-agnostic CRDT op. The same op applies whether merged locally
/// (Store) or via the excluded `gitstate-sync` crate. See §5 for merge rules.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "op")]
pub enum SyncOp {
    // ── Context ──
    ContextLww {
        id: ContextId,
        field: CtxField,
        value: String,
        hlc: Hlc,
    },
    ContextTag {
        id: ContextId,
        tag: String,
        add: bool,
        hlc: Hlc,
    },
    ContextRepo {
        id: ContextId,
        repo_id: RepoId,
        add: bool,
        hlc: Hlc,
    },
    ContextPr {
        id: ContextId,
        repo_slug: String,
        number: u64,
        note: Option<String>,
        add: bool,
        hlc: Hlc,
    },
    ContextDel {
        id: ContextId,
        hlc: Hlc,
    },
    // ── Category ──
    CategoryLww {
        id: CategoryId,
        key: String,
        field: CatField,
        value: String,
        hlc: Hlc,
    },
    CategoryDel {
        id: CategoryId,
        hlc: Hlc,
    },
}

impl SyncOp {
    /// The clock carried by this op (its position in the total order).
    pub fn hlc(&self) -> &Hlc {
        match self {
            SyncOp::ContextLww { hlc, .. }
            | SyncOp::ContextTag { hlc, .. }
            | SyncOp::ContextRepo { hlc, .. }
            | SyncOp::ContextPr { hlc, .. }
            | SyncOp::ContextDel { hlc, .. }
            | SyncOp::CategoryLww { hlc, .. }
            | SyncOp::CategoryDel { hlc, .. } => hlc,
        }
    }
}
