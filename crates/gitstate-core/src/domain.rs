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
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct Dimensions {
    pub shipped: f64,
    pub review: f64,
    pub effort: f64,
    pub quality: f64,
    pub ownership: f64,
    pub durability: f64,
}

/// Pre-normalization evidence behind [`Dimensions`].
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
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

// ── agent runs (the AI-agent write path) ──
//
// Ported from the Go `store/agent_runs.go` (T11 wave 1). The Go table was
// `org_id`-scoped and read/written under Postgres RLS (`db.WithOrg`); this node
// is single-user and single-machine, so there is exactly one tenant and the
// row simply has no `org_id` at all — see `crates/gitstate-store/migrations/
// 0003_agent_runs.sql` for the full reasoning. Everything else — the fields,
// the closed `human_action` set, the create+filtered-list shape — ports
// directly.

/// The closed set `human_action` accepts. `None` means "no human verdict yet"
/// (the Go column allowed empty-string for the same meaning; here it is
/// genuinely absent instead of an empty variant).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum HumanAction {
    Accepted,
    Edited,
    Reverted,
}

impl HumanAction {
    pub fn as_str(&self) -> &'static str {
        match self {
            HumanAction::Accepted => "accepted",
            HumanAction::Edited => "edited",
            HumanAction::Reverted => "reverted",
        }
    }

    /// Parse a human_action value. Unlike the other `parse`s in this module,
    /// an empty string is not an unknown value — it is the caller's job to
    /// treat "no action yet" as `None` rather than calling this with "".
    pub fn parse(s: &str) -> crate::Result<Self> {
        match s {
            "accepted" => Ok(HumanAction::Accepted),
            "edited" => Ok(HumanAction::Edited),
            "reverted" => Ok(HumanAction::Reverted),
            other => Err(crate::Error::invalid(format!(
                "human_action must be one of accepted, edited, reverted (got {other:?})"
            ))),
        }
    }
}

/// The shape of the change one agent run produced. Deliberately smaller than
/// [`DiffSummary`] (which also carries a work item's title/body/paths for
/// effort judging): an agent run may not correspond to any single work item,
/// so this is just the size an agent reports about its own diff.
#[derive(Debug, Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
pub struct AgentDiffSummary {
    pub additions: u32,
    pub deletions: u32,
    pub changed_files: u32,
}

/// One logged agent run — the agent-native mirror of a human commit: what an
/// AI agent set out to do, the shape of what it changed, whether its own
/// tests passed, and (once a human has looked) their verdict. Feeds
/// attribution + estimation the same way human commits do.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentRun {
    pub id: AgentRunId,
    /// The repo this run worked in, if the caller named one.
    pub repo_id: Option<RepoId>,
    /// The PR this run produced, if any. References `work_items(id)` — PRs
    /// and issues are both `WorkItem`s in this schema, not two separate
    /// tables (see §3 of the port plan), so `pr_id`/`issue_id` share a target.
    pub pr_id: Option<WorkItemId>,
    /// The issue this run addressed, if any. See `pr_id`.
    pub issue_id: Option<WorkItemId>,
    /// Free-text local identity of whoever supervised the run, if the caller
    /// gave one. No FK: there is no `users` table in a single-user app (the
    /// Go column referenced one), so this is a label, not a join key.
    pub supervisor_id: Option<String>,
    pub goal: String,
    pub agent_name: Option<String>,
    pub branch: Option<String>,
    pub diff_summary: AgentDiffSummary,
    pub tests_passed: Option<bool>,
    pub human_action: Option<HumanAction>,
    pub iterations: Option<u32>,
    pub cost_usd: Option<f64>,
    pub created_at: String,
}

/// Default page size for [`crate::traits::Store::list_agent_runs`] when the
/// filter sets no `limit` (mirrors the Go store's `agentRunDefaultLimit`).
pub const AGENT_RUN_DEFAULT_LIMIT: u32 = 50;
/// Hard cap regardless of what the caller asks for (mirrors the Go store's
/// `agentRunMaxLimit`) — an unbounded `list_agent_runs` call must not be able
/// to make the daemon materialize its entire log in one response.
pub const AGENT_RUN_MAX_LIMIT: u32 = 200;

// ── calibration persistence types (T11 port plan, wave 2 — see
// `gitstate_calibrate` for the algorithm these rows feed) ──────────────────
//
// Ported from Go's `store/calibration.go`. `org_id` is dropped from every one
// of these, same reasoning as `AgentRun` above: one tenant, one machine,
// nothing left to scope. The two new tables (`effort_calibration`,
// `effort_accuracy`) and the five new nullable columns on `effort`
// (`predicted_secs`, `actual_secs`, `cohort_key`, `size_bucket`,
// `change_type`) are documented in `crates/gitstate-store/migrations/0004_calibration.sql`.

/// One (cohort_key, difficulty_bucket) cell of the calibration curve: the
/// recency-weighted p25/median/p75/mean lead time (seconds) observed for
/// that cohort at that rounded difficulty, plus the sample count `n` the
/// read path uses to decide whether to trust the cell outright, shrink it
/// toward a coarser prior, or fall back further.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CalibrationCell {
    pub cohort_key: String,
    /// `round(difficulty)`, clamped 1..=10 — see `gitstate_calibrate::curve::difficulty_bucket`.
    pub difficulty_bucket: i64,
    pub median_secs: i64,
    pub p25_secs: i64,
    pub p75_secs: i64,
    pub mean_secs: i64,
    pub n: i64,
    pub updated_at: String,
}

/// Per-cohort estimation accuracy: mean absolute error and bias ratio over
/// every estimate that has both a prediction and an observed actual.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AccuracyRow {
    pub cohort_key: String,
    pub n: i64,
    pub mae_secs: i64,
    /// mean(predicted/actual); < 1 means this cohort tends to under-estimate.
    pub bias_ratio: f64,
    pub updated_at: String,
}

/// One raw (cohort_key, difficulty, actual_secs, merged_at) tuple used to
/// rebuild the calibration curves. Returned ungrouped so the caller can apply
/// recency weighting per (cohort, bucket) — mirrors Go's `store.SampleRow`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CohortSample {
    pub cohort_key: String,
    pub difficulty: f64,
    pub actual_secs: i64,
    pub merged_at: String,
}

/// One merged-PR estimate joined to its observed actual time, used to build
/// the per-cohort accuracy summary. Mirrors Go's `store.EstimateOutcome`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EstimateOutcome {
    pub item_id: WorkItemId,
    pub cohort_key: String,
    pub difficulty: f64,
    pub predicted_secs: Option<f64>,
    pub actual_secs: Option<i64>,
    pub merged_at: String,
}

/// A past merged PR in a cohort, used as a prompt anchor ("ran ~Xh predicted
/// vs ~Yh actual"). Mirrors Go's `store.Exemplar`. Note: as in the Go
/// reference, nothing currently calls `Store::list_exemplars` in a live path
/// — `internal/llm/service.go` has its own separate `Exemplar` type fed by a
/// caller that never calls `store.ListExemplars` either. Ported for parity,
/// not because a wired caller exists yet.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Exemplar {
    pub title: String,
    pub difficulty: f64,
    pub predicted_secs: Option<f64>,
    pub actual_secs: Option<i64>,
}

/// The full `effort` row for one item: the base judgement plus whatever
/// calibration has (or hasn't yet) written onto it. A separate type from
/// [`EffortEstimate`] rather than new fields on it — this wave only *reads*
/// the calibration columns (`crates/gitstate-store/migrations/0004_calibration.sql`);
/// every existing caller of `save_effort`/`list_effort`/`EffortEstimate` is
/// left untouched. Backs [`EstimateBrief`] in [`crate::traits::Store::get_effort`].
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EffortRow {
    pub item_id: WorkItemId,
    pub difficulty: f64,
    pub method: EffortMethod,
    pub rationale: String,
    pub confidence: f64,
    /// Calibrated estimate at write time, if calibration has ever run for
    /// this item (nothing currently writes this column live — see the
    /// migration's caveat — so today this is `None` for every row).
    pub predicted_secs: Option<f64>,
    /// Observed lead time, backfilled by [`crate::traits::Store::backfill_actual_secs`].
    pub actual_secs: Option<i64>,
    pub cohort_key: Option<String>,
    pub size_bucket: Option<String>,
    pub change_type: Option<String>,
}

// ── context bundle (T11 port plan, wave 3 — see `gitstate_daemon::ops` for the
// read-only assembly logic that builds these; this module only carries the
// shapes) ──────────────────────────────────────────────────────────────────
//
// Ported from Go's `store/context_bundle.go`. Two deliberate departures from
// a literal port, both forced by choices earlier waves already made, not
// invented here:
// - `codeAreas` no longer reads a `task_files` table: no such table exists in
//   Rust and none is planned (`docs/PORT-PLAN.md` §3 — `task_files` backed a
//   planning feature with zero live callers even in Go). It is re-derived
//   from `WorkItem.files_touched` via `gitstate_calibrate::cohort::top_dirs_from_paths`.
// - `IssueSummary` drops `assigneeId`: `WorkItem` (wave 0's schema) has no
//   assignee field, only `author_login` — a different person in general.
//   Inventing a value here would misattribute work, so the field is dropped
//   rather than faked.
//
// `EstimateBrief.predicted_secs` is a genuine departure in behaviour, not
// just shape: Go's own `predicted_secs` column has no live writer anywhere in
// the Go tree either (grep confirms `EstimateForPR`, the function its own
// comment says populates it, does not exist), so a literal port would read a
// column that is always NULL forever. Since wave 2 landed `gitstate_calibrate`
// specifically so this wave could do better, the assembly logic computes a
// REAL calibrated value live (`gitstate_calibrate::curve::calibrated_secs`)
// whenever a persisted value is absent, rather than porting the Go column
// read verbatim into a permanent no-op. See `gitstate_daemon::ops::build_estimate_brief`.

/// The curated, token-efficient payload an agent can start work from on one
/// issue: the issue itself, related PRs, recent commits, historically-touched
/// code areas, and similar past issues (with how they were resolved).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IssueContextBundle {
    pub issue: IssueSummary,
    pub estimate: Option<EstimateBrief>,
    pub related_prs: Vec<PrBrief>,
    pub recent_commits: Vec<CommitBrief>,
    pub code_areas: Vec<String>,
    pub similar_issues: Vec<SimilarIssue>,
}

/// The trimmed issue itself — `body` is capped so the bundle fits an agent's
/// context window (see `gitstate_daemon::ops::BODY_TRIM_CHARS`).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IssueSummary {
    pub id: WorkItemId,
    /// Parsed from `external_ref` (e.g. `"#123"`); `None` for a native issue
    /// with no platform number.
    pub number: Option<i64>,
    pub title: String,
    pub body: String,
    pub state: String,
    pub labels: Vec<String>,
    pub repo_id: RepoId,
}

/// A one-line PR with the signal an agent cares about: is it merged, and how
/// long did it take.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrBrief {
    pub id: WorkItemId,
    pub number: Option<i64>,
    pub title: String,
    pub state: String,
    pub merged: bool,
    pub lead_time_secs: Option<i64>,
}

/// A short-sha + subject line, the smallest useful trace of recent activity.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommitBrief {
    pub sha: String,
    pub subject: String,
    pub author: String,
    pub is_agent: bool,
}

/// A past issue sharing labels with the one being bundled, plus (best-effort)
/// the PR that resolved it.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SimilarIssue {
    pub id: WorkItemId,
    pub number: Option<i64>,
    pub title: String,
    pub state: String,
    pub shared_labels: Vec<String>,
    pub resolved_by_pr: Option<PrBrief>,
}

/// The calibrated effort estimate for an issue/PR — see the module doc above
/// for why `predicted_secs` is computed live rather than read from a column
/// that no writer (Go or Rust) ever populates.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EstimateBrief {
    pub difficulty: f64,
    pub predicted_secs: Option<f64>,
    pub actual_secs: Option<i64>,
    pub size_bucket: Option<String>,
    pub change_type: Option<String>,
}

/// The curated payload for the PR-context endpoint: diff shape (no raw diff
/// — just its size), cycle time, and the calibrated effort estimate.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrContextBundle {
    pub pr: PrDetail,
    pub diff_summary: PrChangeShape,
    pub cycle_time_secs: Option<i64>,
    pub estimate: Option<EstimateBrief>,
}

/// The trimmed PR header.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrDetail {
    pub id: WorkItemId,
    pub number: Option<i64>,
    pub title: String,
    pub state: String,
    pub merged: bool,
    pub author_login: Option<String>,
    pub merged_at: Option<String>,
}

/// The size of a PR's change — no raw diff, just its shape. `additions`/
/// `deletions` are always 0: `WorkItem` (wave 0's schema) never persisted
/// per-item add/delete counts, only `files_touched` paths (the same
/// documented limitation `gitstate_daemon::ops::effort_items` already lives
/// with for the same reason). `changed_files` is real (`files_touched.len()`).
#[derive(Debug, Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
pub struct PrChangeShape {
    pub additions: u32,
    pub deletions: u32,
    pub changed_files: u32,
}

// ──────────────────────────── search (T11 wave 4) ────────────────────────────
//
// Ported from Go's `store/search.go` + `store/embeddings.go` — hybrid
// full-text/vector/fuzzy search over issues, PRs, and commits. See
// `gitstate_search`'s crate doc for the fuzzy-fallback and FTS5 spike
// writeup; these are just the wire types shared between the store and the
// search crate (same split `AgentRun`/`CalibrationCell` already use).

/// Search result entity kinds. Mirrors Go's `SearchTypeIssue`/`SearchTypePR`/
/// `SearchTypeCommit` string constants as a real enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SearchKind {
    Issue,
    Pr,
    Commit,
}

impl SearchKind {
    pub fn as_str(&self) -> &'static str {
        match self {
            SearchKind::Issue => "issue",
            SearchKind::Pr => "pr",
            SearchKind::Commit => "commit",
        }
    }

    /// Accepts the API-facing plural aliases too (`issues`, `prs`,
    /// `commits`), matching Go's `searchTypeAliases`. Unknown input is
    /// simply not matched (`None`) — the caller drops it, same as Go's
    /// "unrecognised type is ignored" rule.
    pub fn parse(s: &str) -> Option<Self> {
        match s.trim().to_ascii_lowercase().as_str() {
            "issue" | "issues" => Some(SearchKind::Issue),
            "pr" | "prs" => Some(SearchKind::Pr),
            "commit" | "commits" => Some(SearchKind::Commit),
            _ => None,
        }
    }
}

/// One hybrid-search hit: from full-text (FTS5/bm25), vector KNN, RRF fusion
/// of the two, or the fuzzy fallback. Mirrors Go's `store.SearchResult`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SearchHit {
    #[serde(rename = "type")]
    pub kind: SearchKind,
    pub id: String,
    /// Issue/PR number recovered from `external_ref`; `None` for commits or
    /// a native item with no leading digits. Go's `SearchResult.Number` was
    /// a plain `int` defaulting to 0 for "no number" — replaced here with an
    /// explicit optional, the same convention wave 3 already established for
    /// `PrBrief`/`SimilarIssue`, rather than porting an ambiguous zero.
    pub number: Option<i64>,
    pub title: String,
    pub snippet: String,
    /// Whichever ranker produced this hit: FTS5's `bm25()` (sign-flipped so
    /// higher is better, matching cosine's convention), the RRF fused score,
    /// or the fuzzy trigram similarity. Comparable only within one response,
    /// never across calls or ranking modes.
    pub rank: f64,
    pub repo_id: String,
    /// `""` for commits, matching Go.
    pub state: String,
}

/// One issue whose embedding is missing or stale. Mirrors Go's
/// `store.IssueForEmbedding`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PendingEmbedding {
    pub id: WorkItemId,
    pub title: String,
    pub body: String,
}
