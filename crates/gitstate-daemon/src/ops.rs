//! The domain operations behind the API. Both the HTTP route handlers and the
//! CLI call these directly, so `gitstate serve` and `gitstate scan …` share one
//! code path. Pure orchestration over the domain crates — no HTTP, no clap.

use std::path::Path;

use gitstate_classify::Personalizer;
use gitstate_core::analytics as analytics_lib;
use gitstate_core::{
    now_rfc3339, now_wall_ms, Category, CategorySource, Classification, Context, ContextId,
    ContextPrRef, Contribution, Contributor, EffortEstimate, Error, Forge, Hlc, PeerId,
    ProjectState, Repo, RepoId, Result, Store, SyncStatus, Taxonomy, Weights, WorkItem, WorkItemId,
};
use gitstate_git::{
    blame_survival, collect_contributors, default_branch, derive_contributions,
    derive_project_state, head_sha, open_repo, szz_bug_intros, walk_commits, WalkOpts,
};

use crate::dto::{ContextPatch, NewCategory, NewContext, ScanResult};
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
    let engine = state.sync.as_ref().ok_or(Error::SyncDisabled)?;
    let ops = state.store.sync_ops_since(since.as_ref())?;
    let n = ops.len() as u32;
    engine.publish(&ops).await?;
    Ok(n)
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
