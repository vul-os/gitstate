//! The domain operations behind the API. Both the HTTP route handlers and the
//! CLI call these directly, so `gitstate serve` and `gitstate scan …` share one
//! code path. Pure orchestration over the domain crates — no HTTP, no clap.

use std::path::Path;

use gitstate_classify::Personalizer;
use gitstate_core::analytics as analytics_lib;
use gitstate_core::{
    now_rfc3339, now_wall_ms, AgentRun, AgentRunFilter, AgentRunId, Category, CategorySource,
    Classification, Context, ContextId, ContextPrRef, Contribution, Contributor, EffortEstimate,
    Error, Forge, Hlc, HumanAction, PeerId, ProjectState, Repo, RepoId, Result, Store, SyncStatus,
    Taxonomy, Weights, WorkItem, WorkItemId,
};
use gitstate_git::{
    blame_survival, collect_contributors, default_branch, derive_contributions,
    derive_project_state, head_sha, open_repo, szz_bug_intros, walk_commits, WalkOpts,
};

use crate::dto::{AgentRunQuery, ContextPatch, NewAgentRun, NewCategory, NewContext, ScanResult};
use crate::state::AppState;

/// All-time window bounds. Contributions are persisted under this one window so
/// a query with no explicit `from`/`to` always resolves them.
pub const WINDOW_FROM: &str = "1970-01-01T00:00:00Z";
pub const WINDOW_TO: &str = "9999-12-31T23:59:59Z";

// ─────────────────────────────── repos ───────────────────────────────

/// Register a repo from a local path (preferred — enables git scans) or a bare
/// remote URL (metadata only until cloned locally).
pub fn add_repo(
    state: &AppState,
    path: Option<String>,
    remote_url: Option<String>,
) -> Result<Repo> {
    let now = now_rfc3339();
    let repo = if let Some(raw_path) = path.filter(|p| !p.is_empty()) {
        let abs = std::fs::canonicalize(&raw_path)
            .map_err(|e| Error::invalid(format!("path {raw_path}: {e}")))?;
        let gr = open_repo(&abs)?;
        let branch = default_branch(&gr).unwrap_or_else(|_| "main".to_string());
        let forge = remote_url
            .as_deref()
            .map(gitstate_forge::detect_forge)
            .unwrap_or(Forge::Local);
        let slug = match remote_url.as_deref() {
            Some(url) => gitstate_forge::slug_from_remote(url).unwrap_or_else(|_| dir_name(&abs)),
            None => dir_name(&abs),
        };
        Repo {
            id: RepoId::new(),
            slug,
            path: abs.display().to_string(),
            remote_url,
            forge,
            default_branch: branch,
            last_scanned_at: None,
            added_at: now,
        }
    } else if let Some(url) = remote_url.filter(|u| !u.is_empty()) {
        let forge = gitstate_forge::detect_forge(&url);
        let slug = gitstate_forge::slug_from_remote(&url).unwrap_or_else(|_| url.clone());
        Repo {
            id: RepoId::new(),
            slug,
            path: String::new(),
            remote_url: Some(url),
            forge,
            default_branch: "main".to_string(),
            last_scanned_at: None,
            added_at: now,
        }
    } else {
        return Err(Error::invalid("provide either `path` or `remote_url`"));
    };
    state.store.upsert_repo(&repo)?;
    Ok(repo)
}

fn dir_name(p: &Path) -> String {
    p.file_name()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_else(|| p.display().to_string())
}

pub fn list_repos(state: &AppState) -> Result<Vec<Repo>> {
    state.store.list_repos()
}

pub fn get_repo(state: &AppState, id: &RepoId) -> Result<Repo> {
    state
        .store
        .get_repo(id)?
        .ok_or_else(|| Error::not_found("repo", id.0.clone()))
}

pub fn delete_repo(state: &AppState, id: &RepoId) -> Result<()> {
    state.store.delete_repo(id)
}

// ─────────────────────────────── scan ───────────────────────────────

/// Walk git history (+ optionally pull a forge snapshot), derive the project
/// state and the six-dimension contributions, and persist all caches.
pub async fn scan_repo(
    state: &AppState,
    repo_id: &RepoId,
    with_forge: bool,
    since: Option<String>,
) -> Result<ScanResult> {
    let repo = get_repo(state, repo_id)?;
    if repo.path.is_empty() {
        return Err(Error::invalid(
            "repo has no local worktree; re-add it with a local `path` to scan git",
        ));
    }

    // Forge snapshot first (async), so the blocking git work has it in hand.
    let mut forge_items: Vec<WorkItem> = Vec::new();
    let mut warnings: Vec<String> = Vec::new();
    if with_forge && repo.forge != Forge::Local && !repo.slug.is_empty() {
        let client = state.forge.client(repo.forge);
        let s = since.as_deref();
        match client.list_pull_requests(&repo.slug, s).await {
            Ok(mut v) => forge_items.append(&mut v),
            Err(e) => warnings.push(format!("pull requests: {e}")),
        }
        match client.list_issues(&repo.slug, s).await {
            Ok(mut v) => forge_items.append(&mut v),
            Err(e) => warnings.push(format!("issues: {e}")),
        }
        match client.list_reviews(&repo.slug, s).await {
            Ok(mut v) => forge_items.append(&mut v),
            Err(e) => warnings.push(format!("reviews: {e}")),
        }
    }

    let store = state.store.clone();
    let weights = load_weights(state.store.as_ref())?;
    let rid = repo_id.clone();
    let path = repo.path.clone();
    let branch = repo.default_branch.clone();
    let scan_since = since.clone();

    // Git derivation + persistence is CPU-bound and blocking; keep it off the
    // async executor.
    let (mut result, warn) = tokio::task::spawn_blocking(move || {
        derive_and_store(
            store.as_ref(),
            &rid,
            &path,
            &branch,
            scan_since,
            forge_items,
            &weights,
        )
    })
    .await
    .map_err(|e| Error::git(format!("scan task join: {e}")))??;

    warnings.extend(warn);
    // Mark the repo scanned.
    let mut updated = repo;
    updated.last_scanned_at = Some(now_rfc3339());
    state.store.upsert_repo(&updated)?;

    result.warnings = warnings;
    Ok(result)
}

#[allow(clippy::too_many_arguments)]
fn derive_and_store(
    store: &dyn Store,
    repo_id: &RepoId,
    path: &str,
    branch: &str,
    since: Option<String>,
    forge_items: Vec<WorkItem>,
    weights: &gitstate_core::Weights,
) -> Result<(ScanResult, Vec<String>)> {
    let mut warnings = Vec::new();
    let gr = open_repo(Path::new(path))?;

    let opts = WalkOpts {
        since,
        branch: Some(branch.to_string()),
        ..WalkOpts::default()
    };
    let commits = walk_commits(&gr, &opts, repo_id)?;
    store.save_commits(repo_id, &commits)?;

    let survival = match blame_survival(&gr, &opts) {
        Ok(v) => v,
        Err(e) => {
            warnings.push(format!("blame skipped: {e}"));
            Vec::new()
        }
    };
    let bug_intros = match szz_bug_intros(&gr, &opts) {
        Ok(v) => v,
        Err(e) => {
            warnings.push(format!("szz skipped: {e}"));
            Vec::new()
        }
    };

    if !forge_items.is_empty() {
        store.save_work_items(&forge_items)?;
    }

    let contributors = collect_contributors(&commits, &forge_items);
    for c in &contributors {
        store.upsert_contributor(c)?;
    }

    let project_state = derive_project_state(&gr, repo_id, &forge_items)?;
    store.save_project_state(&project_state)?;
    warnings.extend(project_state.warnings.iter().cloned());

    let contributions = derive_contributions(
        &commits,
        &survival,
        &bug_intros,
        &forge_items,
        &[],
        &contributors,
        weights,
        WINDOW_FROM,
        WINDOW_TO,
        repo_id,
    )?;
    store.save_contributions(&contributions)?;

    let head = head_sha(&gr)?.unwrap_or_default();
    let result = ScanResult {
        repo_id: repo_id.clone(),
        head_sha: head,
        commits_scanned: commits.len() as u32,
        contributors: contributors.len() as u32,
        work_items: forge_items.len() as u32,
        project_state,
        warnings: Vec::new(),
    };
    Ok((result, warnings))
}

// ──────────────────────────── derived reads ────────────────────────────

pub fn project_state(state: &AppState, repo_id: &RepoId) -> Result<ProjectState> {
    state
        .store
        .get_project_state(repo_id)?
        .ok_or_else(|| Error::not_found("project_state", repo_id.0.clone()))
}

pub fn contributions(
    state: &AppState,
    repo_id: &RepoId,
    from: Option<&str>,
    to: Option<&str>,
) -> Result<Vec<Contribution>> {
    state.store.get_contributions(
        repo_id,
        from.unwrap_or(WINDOW_FROM),
        to.unwrap_or(WINDOW_TO),
    )
}

pub fn work_items(
    state: &AppState,
    repo_id: &RepoId,
    kind: Option<&str>,
    work_state: Option<&str>,
) -> Result<Vec<WorkItem>> {
    let all = state.store.list_work_items(repo_id)?;
    Ok(all
        .into_iter()
        .filter(|w| kind.is_none_or(|k| w.kind.as_str() == k))
        .filter(|w| work_state.is_none_or(|s| w.state.as_str() == s))
        .collect())
}

pub fn contributors(state: &AppState) -> Result<Vec<Contributor>> {
    state.store.list_contributors()
}

// ──────────────────────────── analytics ────────────────────────────

/// The widest range the UI can ask for. Anything beyond this is clamped so a
/// bogus `?days=999999` can't make the daemon materialize a million-day grid.
pub const MAX_ANALYTICS_DAYS: u32 = 3653; // ~10 years

/// Roll the cached commits + work items up into the visualization payload.
///
/// The window ends at the most recent commit rather than at wall-clock "now":
/// a demo database (or a repo last scanned months ago) would otherwise render
/// an entirely empty heatmap for today's trailing window.
pub fn analytics(
    state: &AppState,
    repo_id: Option<&RepoId>,
    days: Option<u32>,
    from: Option<&str>,
    to: Option<&str>,
) -> Result<gitstate_core::Analytics> {
    let commits = state.store.list_commits(repo_id)?;
    let items = match repo_id {
        Some(r) => state.store.list_work_items(r)?,
        None => state.store.list_all_work_items()?,
    };
    let known = state.store.list_contributors()?;
    let repos = match repo_id {
        Some(_) => 1,
        None => state.store.list_repos()?.len() as u32,
    };

    // Anchor: explicit `to`, else the newest commit, else today.
    let anchor = to
        .and_then(|t| analytics_lib::day_key(t).map(str::to_string))
        .or_else(|| {
            commits
                .iter()
                .filter_map(|c| analytics_lib::day_key(&c.committed_at))
                .max()
                .map(str::to_string)
        })
        .unwrap_or_else(|| now_rfc3339()[..10].to_string());

    let (start, end) = match from.and_then(|f| analytics_lib::day_key(f).map(str::to_string)) {
        Some(f) => (f, anchor),
        None => {
            let window = days.unwrap_or(180).clamp(1, MAX_ANALYTICS_DAYS);
            analytics_lib::range_ending(&anchor, window)
                .ok_or_else(|| Error::invalid(format!("bad analytics anchor date: {anchor}")))?
        }
    };

    Ok(analytics_lib::compute(
        &commits, &items, &known, repos, &start, &end,
    ))
}

// ───────────────────────── classify + effort ─────────────────────────

fn select_items(
    store: &dyn Store,
    repo_id: &RepoId,
    item_ids: Option<Vec<String>>,
    only_uncategorized: bool,
) -> Result<Vec<WorkItem>> {
    let all = store.list_work_items(repo_id)?;
    let items = match item_ids {
        Some(refs) => all
            .into_iter()
            .filter(|w| refs.contains(&w.external_ref) || refs.contains(&w.id.0))
            .collect(),
        None if only_uncategorized => all
            .into_iter()
            .filter(|w| store.get_classification(&w.id).ok().flatten().is_none())
            .collect(),
        None => all,
    };
    Ok(items)
}

pub async fn classify_items(
    state: &AppState,
    repo_id: &RepoId,
    item_ids: Option<Vec<String>>,
) -> Result<Vec<Classification>> {
    let items = select_items(state.store.as_ref(), repo_id, item_ids, true)?;
    if items.is_empty() {
        return Ok(Vec::new());
    }
    let base = state.classifier.classify(&items, &state.taxonomy).await?;
    let adjusted = Personalizer::new(state.store.as_ref()).adjust(&base)?;
    state.store.save_classifications(&adjusted)?;
    Ok(adjusted)
}

pub fn record_feedback(state: &AppState, item_id: &WorkItemId, category_key: &str) -> Result<()> {
    state.store.record_feedback(item_id, category_key)?;
    Personalizer::new(state.store.as_ref()).record(item_id, category_key)
}

pub async fn effort_items(
    state: &AppState,
    repo_id: &RepoId,
    item_ids: Option<Vec<String>>,
) -> Result<Vec<EffortEstimate>> {
    let items = select_items(state.store.as_ref(), repo_id, item_ids, false)?;
    if items.is_empty() {
        return Ok(Vec::new());
    }
    // Diff shape is derived from work-item metadata (files touched + text).
    // Exact add/del counts require resolving each PR's base/head against the
    // local worktree — a follow-up; the heuristic degrades gracefully with the
    // file/path signal alone.
    let diffs: Vec<gitstate_core::DiffSummary> = items
        .iter()
        .map(|w| gitstate_core::DiffSummary {
            item_id: w.id.clone(),
            external_ref: w.external_ref.clone(),
            additions: 0,
            deletions: 0,
            files: w.files_touched.len() as u32,
            languages: languages_of(&w.files_touched),
            touched_paths: w.files_touched.clone(),
            title: w.title.clone(),
            body: w.body.clone(),
        })
        .collect();
    let est = state.classifier.judge_effort(&diffs).await?;
    state.store.save_effort(&est)?;
    Ok(est)
}

fn languages_of(paths: &[String]) -> Vec<String> {
    let mut langs: Vec<String> = Vec::new();
    for p in paths {
        if let Some(lang) = language_for(p) {
            if !langs.iter().any(|l| l == lang) {
                langs.push(lang.to_string());
            }
        }
    }
    langs
}

fn language_for(path: &str) -> Option<&'static str> {
    let ext = path.rsplit('.').next()?;
    Some(match ext {
        "rs" => "Rust",
        "go" => "Go",
        "ts" | "tsx" => "TypeScript",
        "js" | "jsx" | "mjs" | "cjs" => "JavaScript",
        "py" => "Python",
        "java" => "Java",
        "kt" => "Kotlin",
        "swift" => "Swift",
        "c" | "h" => "C",
        "cc" | "cpp" | "hpp" | "cxx" => "C++",
        "rb" => "Ruby",
        "php" => "PHP",
        "cs" => "C#",
        "sql" => "SQL",
        "sh" | "bash" | "zsh" => "Shell",
        "css" | "scss" | "sass" => "CSS",
        "html" | "htm" => "HTML",
        "md" | "mdx" => "Markdown",
        "yml" | "yaml" => "YAML",
        "json" => "JSON",
        "toml" => "TOML",
        _ => return None,
    })
}

// ──────────────────────────── contexts ────────────────────────────

fn placeholder_hlc() -> Hlc {
    // The store re-mints its own HLC on upsert; this is only a placeholder to
    // satisfy the struct.
    Hlc {
        wall_ms: now_wall_ms(),
        counter: 0,
        peer: PeerId::from("local"),
    }
}

pub fn list_contexts(state: &AppState) -> Result<Vec<Context>> {
    state.store.list_contexts()
}

pub fn get_context(state: &AppState, id: &ContextId) -> Result<Context> {
    state
        .store
        .get_context(id)?
        .ok_or_else(|| Error::not_found("context", id.0.clone()))
}

pub fn create_context(state: &AppState, req: NewContext) -> Result<Context> {
    let now = now_rfc3339();
    let ctx = Context {
        id: ContextId::new(),
        name: req.name,
        description: req.description.unwrap_or_default(),
        repo_ids: req
            .repo_ids
            .unwrap_or_default()
            .into_iter()
            .map(RepoId::from)
            .collect(),
        pr_refs: req.pr_refs.unwrap_or_default(),
        notes: req.notes.unwrap_or_default(),
        tags: req.tags.unwrap_or_default(),
        created_at: now.clone(),
        updated_at: now,
        hlc: placeholder_hlc(),
        deleted: false,
    };
    state.store.upsert_context(&ctx)?;
    get_context(state, &ctx.id)
}

pub fn patch_context(state: &AppState, id: &ContextId, patch: ContextPatch) -> Result<Context> {
    let mut ctx = get_context(state, id)?;
    if let Some(v) = patch.name {
        ctx.name = v;
    }
    if let Some(v) = patch.description {
        ctx.description = v;
    }
    if let Some(v) = patch.notes {
        ctx.notes = v;
    }
    if let Some(v) = patch.repo_ids {
        ctx.repo_ids = v.into_iter().map(RepoId::from).collect();
    }
    if let Some(v) = patch.pr_refs {
        ctx.pr_refs = v;
    }
    if let Some(v) = patch.tags {
        ctx.tags = v;
    }
    ctx.updated_at = now_rfc3339();
    state.store.upsert_context(&ctx)?;
    get_context(state, id)
}

pub fn delete_context(state: &AppState, id: &ContextId) -> Result<()> {
    let mut ctx = get_context(state, id)?;
    ctx.deleted = true;
    ctx.updated_at = now_rfc3339();
    state.store.upsert_context(&ctx)
}

/// Import a full context object (from `context export`), minting a fresh id.
pub fn import_context(state: &AppState, mut ctx: Context) -> Result<Context> {
    ctx.id = ContextId::new();
    ctx.hlc = placeholder_hlc();
    ctx.deleted = false;
    let now = now_rfc3339();
    if ctx.created_at.is_empty() {
        ctx.created_at = now.clone();
    }
    ctx.updated_at = now;
    state.store.upsert_context(&ctx)?;
    get_context(state, &ctx.id)
}

// ──────────────────────────── categories ────────────────────────────

pub fn list_categories(state: &AppState) -> Result<Vec<Category>> {
    state.store.list_categories()
}

pub fn create_category(state: &AppState, req: NewCategory) -> Result<Category> {
    let cat = Category {
        id: gitstate_core::CategoryId::new(),
        key: req.key,
        label: req.label,
        parent_key: req.parent_key,
        color: req.color,
        source: CategorySource::Local,
        taxonomy_version: None,
        hlc: placeholder_hlc(),
        deleted: false,
    };
    state.store.upsert_category(&cat)?;
    state
        .store
        .get_category(&cat.key)?
        .ok_or_else(|| Error::not_found("category", cat.key.clone()))
}

fn find_category_by_id(state: &AppState, id: &str) -> Result<Category> {
    state
        .store
        .list_categories()?
        .into_iter()
        .find(|c| c.id.0 == id)
        .ok_or_else(|| Error::not_found("category", id.to_string()))
}

pub fn patch_category(
    state: &AppState,
    id: &str,
    label: Option<String>,
    color: Option<String>,
    parent_key: Option<String>,
) -> Result<Category> {
    let mut cat = find_category_by_id(state, id)?;
    if let Some(v) = label {
        cat.label = v;
    }
    if let Some(v) = color {
        cat.color = Some(v);
    }
    if let Some(v) = parent_key {
        cat.parent_key = Some(v);
    }
    let key = cat.key.clone();
    state.store.upsert_category(&cat)?;
    state
        .store
        .get_category(&key)?
        .ok_or_else(|| Error::not_found("category", key))
}

pub fn delete_category(state: &AppState, id: &str) -> Result<()> {
    let mut cat = find_category_by_id(state, id)?;
    cat.deleted = true;
    state.store.upsert_category(&cat)
}

// ──────────────────────────── agent runs ────────────────────────────
//
// Ported from Go's `store/agent_runs.go` (T11 port plan, wave 1). The Go
// version validated `human_action` and org-scoped every read/write inside a
// `db.WithOrg` RLS transaction; there is no second tenant on one person's own
// machine, so the org scoping is simply gone (see `AgentRun`'s doc comment
// and migration 0003 for the full reasoning) — the validation still applies
// verbatim.

/// Record one agent run. `req.goal` is the only required field; every other
/// field is optional and left absent when the caller doesn't supply it.
///
/// Returns [`Error::Invalid`] if `goal` is blank or `human_action` is set to
/// anything outside the closed set (`accepted`/`edited`/`reverted`) — the
/// same rejection Go's `CreateAgentRun` returned as `ErrInvalidHumanAction`.
pub fn log_agent_run(state: &AppState, req: NewAgentRun) -> Result<AgentRun> {
    if req.goal.trim().is_empty() {
        return Err(Error::invalid("goal is required"));
    }
    let human_action = match req.human_action.as_deref() {
        None => None,
        Some(s) if s.trim().is_empty() => None,
        Some(s) => Some(HumanAction::parse(s)?),
    };
    let run = AgentRun {
        id: AgentRunId::new(),
        repo_id: req.repo_id.filter(|s| !s.is_empty()).map(RepoId::from),
        pr_id: req.pr_id.filter(|s| !s.is_empty()).map(WorkItemId::from),
        issue_id: req.issue_id.filter(|s| !s.is_empty()).map(WorkItemId::from),
        supervisor_id: req.supervisor_id.filter(|s| !s.is_empty()),
        goal: req.goal,
        agent_name: req.agent_name.filter(|s| !s.is_empty()),
        branch: req.branch.filter(|s| !s.is_empty()),
        diff_summary: req.diff_summary.unwrap_or_default(),
        tests_passed: req.tests_passed,
        human_action,
        iterations: req.iterations,
        cost_usd: req.cost_usd,
        created_at: now_rfc3339(),
    };
    state.store.create_agent_run(&run)?;
    Ok(run)
}

/// Agent runs newest-first, narrowed by whichever of `q`'s fields are set.
pub fn list_agent_runs(state: &AppState, q: AgentRunQuery) -> Result<Vec<AgentRun>> {
    let filter = AgentRunFilter {
        repo_id: q.repo_id.filter(|s| !s.is_empty()).map(RepoId::from),
        pr_id: q.pr_id.filter(|s| !s.is_empty()).map(WorkItemId::from),
        issue_id: q.issue_id.filter(|s| !s.is_empty()).map(WorkItemId::from),
        agent_name: q.agent_name.filter(|s| !s.is_empty()),
        limit: q.limit,
    };
    state.store.list_agent_runs(&filter)
}

// ──────────────────────────── context bundle ────────────────────────────
//
// Ported from Go's `store/context_bundle.go` (T11 port plan, wave 3): curated,
// token-efficient issue/PR context for an agent starting work. Called both by
// `gitstate agent context`/`gitstate agent pr` (CLI, in-process — see
// `gitstate_cli::cmd::agent`) and by the `get_issue`/`get_pr_context` MCP
// tools (`gitstate_cli::cmd::mcp`), the same in-process pattern wave 1
// established. No HTTP route: nothing consumes one — see the doc on
// `IssueContextBundle` in `gitstate_core::domain` for the two shape
// departures this wave makes deliberately (dropped `assigneeId`, re-derived
// `codeAreas`), and why `EstimateBrief.predicted_secs` is computed LIVE via
// `gitstate_calibrate` (wave 2) rather than read from a column nothing, in
// either language, ever writes.

const BUNDLE_MAX_RELATED_PRS: usize = 5;
const BUNDLE_MAX_RELATED_COMMITS: usize = 8;
const BUNDLE_MAX_SIMILAR_ISSUES: usize = 3;
const BUNDLE_MAX_CODE_AREAS: usize = 10;
const BUNDLE_BODY_TRIM_CHARS: usize = 800;
const BUNDLE_TITLE_TRIM_CHARS: usize = 100;

/// Trims whitespace then caps to `n` **characters** (not bytes — Go's
/// `s[:n]` byte-slices, which can split a multi-byte rune; this is a
/// deliberate, safer departure, not a faithfully-ported bug), appending an
/// ellipsis when truncated.
fn bundle_trim(s: &str, n: usize) -> String {
    let s = s.trim();
    if s.chars().count() <= n {
        return s.to_string();
    }
    let head: String = s.chars().take(n).collect();
    format!("{}…", head.trim_end())
}

/// First 10 characters of a sha, matching Go's `shortSHA`.
fn bundle_short_sha(sha: &str) -> String {
    if sha.chars().count() > 10 {
        sha.chars().take(10).collect()
    } else {
        sha.to_string()
    }
}

/// Best-effort platform number recovered from an `external_ref` like `"#123"`
/// or `"!45"`. Go's `Issue.Number`/`PullRequest.Number` were their own int
/// columns; `WorkItem` (wave 0's schema) has none, so this re-derives the
/// same value from the ref string every forge client already formats it into
/// (`gitstate_forge::github`/`gitlab`). `None` for a native/local item with
/// no leading digits.
fn bundle_parse_number(external_ref: &str) -> Option<i64> {
    let rest = external_ref.trim_start_matches(|c: char| !c.is_ascii_digit());
    let digits: String = rest.chars().take_while(|c| c.is_ascii_digit()).collect();
    digits.parse::<i64>().ok().filter(|n| *n > 0)
}

/// The diff-shape inputs `gitstate_calibrate::cohort` needs, degraded the
/// same documented way `effort_items` already is: `WorkItem` never persisted
/// add/delete counts, only `files_touched` paths, so churn is always 0 and
/// only the file-count/top-dirs signals are real.
fn bundle_diff_shape(
    item: &WorkItem,
) -> (
    gitstate_calibrate::cohort::PrMeta,
    gitstate_calibrate::cohort::DiffStats,
) {
    let top_dirs = gitstate_calibrate::cohort::top_dirs_from_paths(&item.files_touched);
    let stats = gitstate_calibrate::cohort::DiffStats {
        additions: 0,
        deletions: 0,
        changed_files: item.files_touched.len() as u32,
        top_dirs,
    };
    let meta = gitstate_calibrate::cohort::PrMeta {
        repo_id: item.repo_id.0.clone(),
        title: item.title.clone(),
        branch: String::new(),
        paths: item.files_touched.clone(),
    };
    (meta, stats)
}

/// Builds the calibrated `EstimateBrief` for one item, or `None` if it has
/// never been judged (no `effort` row — matches Go's behaviour when
/// `effort_estimates` has no row for the issue/PR). When calibration has not
/// yet persisted `predicted_secs`/`size_bucket`/`change_type` for this item
/// (true for every row today — see the migration's caveat and the crate doc
/// on `gitstate_calibrate`), they are computed LIVE here via the calibration
/// crate rather than left null: this is the wave's whole payoff, not a stub.
fn build_estimate_brief(
    state: &AppState,
    item: &WorkItem,
) -> Result<Option<gitstate_core::EstimateBrief>> {
    let Some(row) = state.store.get_effort(&item.id)? else {
        return Ok(None);
    };

    let actual_secs = row.actual_secs.or_else(|| {
        item.merged_at
            .as_deref()
            .and_then(|m| analytics_lib::lead_time_secs(&item.created_at, m))
    });

    let (meta, stats) = bundle_diff_shape(item);
    let change_type = row
        .change_type
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| gitstate_calibrate::cohort::change_type(&meta).to_string());
    let size_bucket = row
        .size_bucket
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| gitstate_calibrate::cohort::size_bucket(&stats).to_string());

    let predicted_secs = match row.predicted_secs {
        Some(p) => Some(p),
        None => {
            let candidates =
                gitstate_calibrate::cohort::cohort_candidates(&meta, &stats, &change_type);
            gitstate_calibrate::curve::calibrated_secs(
                state.store.as_ref(),
                row.difficulty,
                &candidates,
            )
            .ok()
            .map(|r| r.predicted_secs)
        }
    };

    Ok(Some(gitstate_core::EstimateBrief {
        difficulty: row.difficulty,
        predicted_secs,
        actual_secs,
        size_bucket: Some(size_bucket),
        change_type: Some(change_type),
    }))
}

fn bundle_pr_brief(item: &WorkItem) -> gitstate_core::PrBrief {
    let lead_time_secs = item
        .merged_at
        .as_deref()
        .and_then(|m| analytics_lib::lead_time_secs(&item.created_at, m));
    gitstate_core::PrBrief {
        id: item.id.clone(),
        number: bundle_parse_number(&item.external_ref),
        title: bundle_trim(&item.title, BUNDLE_TITLE_TRIM_CHARS),
        state: item.state.as_str().to_string(),
        merged: item.merged_at.is_some(),
        lead_time_secs,
    }
}

/// Recent PRs in the same repo, newest-first (merged_at, falling back to
/// created_at for unmerged PRs), capped. Mirrors Go's `relatedPRs`.
fn bundle_related_prs(state: &AppState, repo_id: &RepoId) -> Result<Vec<gitstate_core::PrBrief>> {
    let mut prs: Vec<WorkItem> = state
        .store
        .list_work_items(repo_id)?
        .into_iter()
        .filter(|w| w.kind == gitstate_core::WorkKind::Pr)
        .collect();
    prs.sort_by(|a, b| {
        let ka = a.merged_at.as_deref().unwrap_or(&a.created_at);
        let kb = b.merged_at.as_deref().unwrap_or(&b.created_at);
        kb.cmp(ka)
    });
    prs.truncate(BUNDLE_MAX_RELATED_PRS);
    Ok(prs.iter().map(bundle_pr_brief).collect())
}

/// Recent commits in the repo, newest-first, capped. Mirrors Go's
/// `recentCommits`; `isAgent` is derived from the SAME agent-detection
/// heuristic `gitstate-git` already uses when it classifies commit
/// contributors (`gitstate_git::util::detect_agent`), rather than a
/// re-invented one — `Commit` (wave 0's schema) carries no `is_agent` column
/// of its own the way Go's `commits.is_agent` did.
fn bundle_recent_commits(
    state: &AppState,
    repo_id: &RepoId,
) -> Result<Vec<gitstate_core::CommitBrief>> {
    // `list_commits` returns oldest-first; take the newest N off the end.
    let mut commits = state.store.list_commits(Some(repo_id))?;
    let start = commits.len().saturating_sub(BUNDLE_MAX_RELATED_COMMITS);
    let recent = commits.split_off(start);
    Ok(recent
        .into_iter()
        .rev()
        .map(|c| gitstate_core::CommitBrief {
            sha: bundle_short_sha(&c.sha),
            subject: bundle_trim(&c.summary, BUNDLE_TITLE_TRIM_CHARS),
            is_agent: gitstate_git::util::detect_agent(&c.author_name, &c.author_email, None)
                .is_some(),
            author: c.author_name,
        })
        .collect())
}

/// The historically-touched top-level directories in this repo's work items,
/// re-derived from `WorkItem.files_touched` via the same
/// `top_dirs_from_paths` helper `gitstate_calibrate::cohort` uses for cohort
/// keys. Go's `codeAreas` read a `task_files` table that has no Rust
/// equivalent and no live caller even in Go (`docs/PORT-PLAN.md` §3) — this
/// is the re-derivation the plan calls for, not a stub.
fn bundle_code_areas(state: &AppState, repo_id: &RepoId) -> Result<Vec<String>> {
    let items = state.store.list_work_items(repo_id)?;
    let mut paths: Vec<String> = Vec::new();
    for w in &items {
        paths.extend(w.files_touched.iter().cloned());
    }
    let mut areas = gitstate_calibrate::cohort::top_dirs_from_paths(&paths);
    areas.truncate(BUNDLE_MAX_CODE_AREAS);
    Ok(areas)
}

/// The most-recently-merged PR in a repo, if any. Mirrors the single-repo
/// case of Go's `latestMergedPRsByRepo` (the Go version batches this across
/// every candidate repo in one query to avoid an N+1; at this domain's local,
/// hundreds-of-items scale, `list_work_items` is already an in-memory scan
/// per repo, so the N+1 shape costs nothing worth batching for).
fn bundle_latest_merged_pr(
    state: &AppState,
    repo_id: &RepoId,
) -> Result<Option<gitstate_core::PrBrief>> {
    let mut prs: Vec<WorkItem> = state
        .store
        .list_work_items(repo_id)?
        .into_iter()
        .filter(|w| w.kind == gitstate_core::WorkKind::Pr && w.merged_at.is_some())
        .collect();
    prs.sort_by(|a, b| b.merged_at.cmp(&a.merged_at));
    Ok(prs.into_iter().next().map(|w| bundle_pr_brief(&w)))
}

/// Past issues sharing ≥1 label with `iss`, ranked by shared-label count then
/// recency, capped, each with its best-effort "resolved by" PR. Mirrors Go's
/// `similarIssues` — searched across every repo (there is only one tenant to
/// scope to), not just `iss`'s own repo, matching Go's org-wide (not
/// repo-scoped) query.
fn bundle_similar_issues(
    state: &AppState,
    iss: &WorkItem,
) -> Result<Vec<gitstate_core::SimilarIssue>> {
    if iss.labels.is_empty() {
        return Ok(Vec::new());
    }
    let all = state.store.list_all_work_items()?;
    let mut cands: Vec<(WorkItem, Vec<String>)> = all
        .into_iter()
        .filter(|w| w.kind == gitstate_core::WorkKind::Issue && w.id != iss.id)
        .filter_map(|w| {
            let shared: Vec<String> = w
                .labels
                .iter()
                .filter(|l| iss.labels.contains(l))
                .cloned()
                .collect();
            if shared.is_empty() {
                None
            } else {
                Some((w, shared))
            }
        })
        .collect();
    cands.sort_by(|(wa, sa), (wb, sb)| {
        sb.len()
            .cmp(&sa.len())
            .then_with(|| wb.updated_at.cmp(&wa.updated_at))
    });
    cands.truncate(BUNDLE_MAX_SIMILAR_ISSUES);

    let mut out = Vec::with_capacity(cands.len());
    for (w, shared) in cands {
        let resolved_by_pr = bundle_latest_merged_pr(state, &w.repo_id)?;
        out.push(gitstate_core::SimilarIssue {
            id: w.id.clone(),
            number: bundle_parse_number(&w.external_ref),
            title: bundle_trim(&w.title, BUNDLE_TITLE_TRIM_CHARS),
            state: w.state.as_str().to_string(),
            shared_labels: shared,
            resolved_by_pr,
        });
    }
    Ok(out)
}

/// Assembles the curated issue-context bundle. Mirrors Go's
/// `BuildIssueContext`. Errors [`Error::NotFound`] if `issue_id` does not
/// resolve to a work item of kind `issue` (a PR id, or an unknown id, both
/// 404 the same way `GetIssue` did against the issues-only table).
pub fn build_issue_context(
    state: &AppState,
    issue_id: &WorkItemId,
) -> Result<gitstate_core::IssueContextBundle> {
    let iss = state
        .store
        .get_work_item(issue_id)?
        .filter(|w| w.kind == gitstate_core::WorkKind::Issue)
        .ok_or_else(|| Error::not_found("issue", issue_id.0.clone()))?;

    let estimate = build_estimate_brief(state, &iss)?;
    let related_prs = bundle_related_prs(state, &iss.repo_id)?;
    let recent_commits = bundle_recent_commits(state, &iss.repo_id)?;
    let code_areas = bundle_code_areas(state, &iss.repo_id)?;
    let similar_issues = bundle_similar_issues(state, &iss)?;

    Ok(gitstate_core::IssueContextBundle {
        issue: gitstate_core::IssueSummary {
            id: iss.id.clone(),
            number: bundle_parse_number(&iss.external_ref),
            title: iss.title.clone(),
            body: bundle_trim(&iss.body, BUNDLE_BODY_TRIM_CHARS),
            state: iss.state.as_str().to_string(),
            labels: iss.labels.clone(),
            repo_id: iss.repo_id.clone(),
        },
        estimate,
        related_prs,
        recent_commits,
        code_areas,
        similar_issues,
    })
}

/// Assembles the curated PR-context bundle. Mirrors Go's `BuildPRContext`.
pub fn build_pr_context(
    state: &AppState,
    pr_id: &WorkItemId,
) -> Result<gitstate_core::PrContextBundle> {
    let pr = state
        .store
        .get_work_item(pr_id)?
        .filter(|w| w.kind == gitstate_core::WorkKind::Pr)
        .ok_or_else(|| Error::not_found("pull request", pr_id.0.clone()))?;

    let cycle_time_secs = pr
        .merged_at
        .as_deref()
        .and_then(|m| analytics_lib::lead_time_secs(&pr.created_at, m));
    let estimate = build_estimate_brief(state, &pr)?;
    let changed_files = pr.files_touched.len() as u32;

    Ok(gitstate_core::PrContextBundle {
        pr: gitstate_core::PrDetail {
            id: pr.id.clone(),
            number: bundle_parse_number(&pr.external_ref),
            title: bundle_trim(&pr.title, BUNDLE_TITLE_TRIM_CHARS),
            state: pr.state.as_str().to_string(),
            merged: pr.merged_at.is_some(),
            author_login: pr.author_login.clone(),
            merged_at: pr.merged_at.clone(),
        },
        diff_summary: gitstate_core::PrChangeShape {
            additions: 0,
            deletions: 0,
            changed_files,
        },
        cycle_time_secs,
        estimate,
    })
}

// ──────────────────────────── taxonomy ────────────────────────────

pub fn taxonomy(state: &AppState) -> Taxonomy {
    (*state.taxonomy).clone()
}

/// Verify a taxonomy doc against the pinned key; returns its content id.
pub fn verify_taxonomy(tax: &Taxonomy) -> Result<String> {
    tax.verify(&gitstate_classify::pinned_pubkey())?;
    Ok(tax.content_id())
}

// ──────────────────────────── sync ────────────────────────────

pub async fn sync_status(state: &AppState) -> Result<SyncStatus> {
    match &state.sync {
        Some(s) => s.status().await,
        None => {
            let peer = state.store.kv_get("peer_id")?.unwrap_or_default();
            Ok(SyncStatus {
                enabled: false,
                peer_id: PeerId::from(peer),
                peers: 0,
                last_op_hlc: None,
            })
        }
    }
}

pub async fn sync_publish(state: &AppState, since: Option<Hlc>) -> Result<u32> {
    let engine = state
        .sync
        .as_ref()
        .ok_or_else(|| Error::invalid("no local sync engine is wired"))?;
    let ops = state.store.sync_ops_since(since.as_ref())?;
    let n = ops.len() as u32;
    engine.publish(&ops).await?;
    Ok(n)
}

/// This node's peer id and public key — the pair an operator gives to the other
/// node so it can enrol this one. The secret half is never returned.
pub fn node_identity_view(state: &AppState) -> Result<crate::dto::NodeIdentityResp> {
    let identity = gitstate_sync::node_identity(state.store.as_ref())?;
    let peer_id = state.store.kv_get("peer_id")?.unwrap_or_default();
    Ok(crate::dto::NodeIdentityResp {
        peer_id,
        pubkey: identity.public_hex(),
    })
}

/// One replication round with every enrolled peer.
///
/// With no peers enrolled this is an explicit refusal rather than a silent
/// success: "synced with nobody" and "synced" must not look the same to an
/// operator who has just set a node up.
pub async fn sync_run(state: &AppState) -> Result<crate::dto::SyncRunResp> {
    if state.store.list_sync_peers()?.is_empty() {
        return Err(Error::SyncNoPeers);
    }
    let node = gitstate_sync::SyncNode::from_store(state.store.clone())?;
    let reports = node.sync_all().await?;
    Ok(crate::dto::SyncRunResp {
        peers: reports
            .into_iter()
            .map(|r| {
                let ingested = r.pull_outcome.unwrap_or_default();
                crate::dto::PeerRoundView {
                    peer_id: r.peer.0,
                    url: r.url,
                    pushed: r.pushed,
                    pulled: r.pulled,
                    applied: ingested.applied,
                    skipped: ingested.skipped,
                    rejected: ingested.rejected,
                    error: r.error,
                }
            })
            .collect(),
    })
}

// ──────────────────────────── weights kv ────────────────────────────

/// Load persisted contribution weights, or the defaults.
pub fn load_weights(store: &dyn Store) -> Result<Weights> {
    match store.kv_get("weights")? {
        Some(json) => {
            Ok(serde_json::from_str(&json).unwrap_or_else(|_| Weights::default_weights()))
        }
        None => Ok(Weights::default_weights()),
    }
}

/// Persist contribution weights.
pub fn save_weights(store: &dyn Store, w: &Weights) -> Result<()> {
    store.kv_set("weights", &serde_json::to_string(w)?)
}

/// Resolve the data directory and database path (honors `GITSTATE_DATA_DIR`).
pub fn data_paths() -> Result<(std::path::PathBuf, std::path::PathBuf)> {
    let dir = gitstate_store::SqliteStore::data_dir()?;
    let db = gitstate_store::db_path(&dir);
    Ok((dir, db))
}

/// Convenience: parse a `ContextPrRef` from a "slug#number" CLI token.
pub fn parse_pr_token(tok: &str) -> Result<ContextPrRef> {
    let (slug, num) = tok
        .rsplit_once('#')
        .ok_or_else(|| Error::invalid(format!("bad pr ref (want slug#number): {tok}")))?;
    let number: u64 = num
        .parse()
        .map_err(|_| Error::invalid(format!("bad pr number: {num}")))?;
    Ok(ContextPrRef {
        repo_slug: slug.to_string(),
        number,
        note: None,
    })
}
