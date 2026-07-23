//! `gitstate seed --demo` — populate a fresh database with a deterministic,
//! fully synthetic demo dataset: a fake org, fake pseudonymous contributors
//! (`@example.com` emails only — never real names/emails), and derived
//! project state/effort/contribution/classification rows shaped like real
//! output. Exists so product screenshots never touch real git/forge history.
//!
//! Every id, hash, and metric is derived from a fixed string key via SHA-256
//! (see [`det_u64`]/[`det_uuid`]/[`det_hex`]) rather than a wall-clock RNG, so
//! re-running the seed against the same database reproduces byte-identical
//! rows (upserts overwrite in place — no duplicate accumulation) and repeated
//! screenshot sessions stay pixel-stable.

use std::path::PathBuf;

use clap::Args;
use sha2::{Digest, Sha256};
use time::format_description::well_known::Rfc3339;
use time::{Duration, OffsetDateTime};

use gitstate_core::{
    taxonomy::default_categories, Category, CategoryId, CategorySource, Classification, Commit,
    Context, ContextId, ContextPrRef, Contribution, Contributor, ContributorId, DimensionRaw,
    Dimensions, EffortEstimate, EffortMethod, Forge, Hlc, PeerId, ProjectState, Repo, RepoId,
    Store, Taxonomy, WorkItem, WorkItemId, WorkKind, WorkState,
};
use gitstate_daemon::ops::{WINDOW_FROM, WINDOW_TO};
use gitstate_store::{db_path, SqliteStore};

use super::Ctx;

#[derive(Debug, Args)]
pub struct SeedArgs {
    /// Populate with the deterministic synthetic demo dataset (fake org,
    /// fake pseudonymous people, no real data). Currently the only
    /// supported seed kind.
    #[arg(long)]
    pub demo: bool,

    /// SQLite database file to write. Defaults to the resolved gitstate data
    /// dir's `gitstate.db` — the same file `gitstate serve` opens — so
    /// `gitstate seed --demo && gitstate serve` shows the demo data with no
    /// extra flags.
    #[arg(long)]
    pub db: Option<PathBuf>,
}

/// Row counts written, for `--json` output and human summary printing.
#[derive(Debug, Default, serde::Serialize)]
pub struct SeedSummary {
    pub repos: usize,
    pub contributors: usize,
    pub categories: usize,
    pub work_items: usize,
    pub commits: usize,
    pub contributions: usize,
    pub classifications: usize,
    pub effort: usize,
    pub contexts: usize,
}

pub fn run(ctx: &Ctx, args: SeedArgs) -> anyhow::Result<()> {
    if !args.demo {
        anyhow::bail!("nothing to seed: pass --demo (the only supported seed kind)");
    }
    let path = match args.db {
        Some(p) => p,
        None => db_path(&SqliteStore::data_dir()?),
    };
    let store = SqliteStore::open(&path)?;
    let summary = seed_demo(&store)?;
    if ctx.json {
        ctx.print_json(&summary)?;
    } else {
        println!("seeded synthetic demo data -> {}", path.display());
        println!("  repos           {}", summary.repos);
        println!("  contributors    {}", summary.contributors);
        println!("  categories      {}", summary.categories);
        println!("  work items      {}", summary.work_items);
        println!("  commits         {}", summary.commits);
        println!("  contributions   {}", summary.contributions);
        println!("  classifications {}", summary.classifications);
        println!("  effort          {}", summary.effort);
        println!("  contexts        {}", summary.contexts);
    }
    Ok(())
}

// ─────────────────────────── deterministic helpers ───────────────────────────

/// A fixed "now" for the demo dataset, so re-seeding on a different day still
/// produces the exact same timestamps (and therefore the exact same
/// screenshots).
const DEMO_ANCHOR: &str = "2026-06-15T09:00:00Z";

fn anchor() -> OffsetDateTime {
    OffsetDateTime::parse(DEMO_ANCHOR, &Rfc3339).expect("valid fixed demo anchor")
}

fn days_ago(days: i64) -> String {
    (anchor() - Duration::days(days.max(0)))
        .format(&Rfc3339)
        .expect("format rfc3339")
}

/// A stable 64-bit value derived from `key` (SHA-256, first 8 bytes, BE).
fn det_u64(key: &str) -> u64 {
    let digest = Sha256::digest(key.as_bytes());
    u64::from_be_bytes(digest[0..8].try_into().expect("8 bytes"))
}

/// A stable integer in `[lo, hi]` derived from `key`.
fn det_range(key: &str, lo: u64, hi: u64) -> u64 {
    lo + det_u64(key) % (hi - lo + 1)
}

/// A stable lowercase hex string of `len` characters derived from `key`.
fn det_hex(key: &str, len: usize) -> String {
    let digest = Sha256::digest(key.as_bytes());
    let full = hex::encode(digest);
    full[..len.min(full.len())].to_string()
}

/// A stable, UUID-shaped id derived from `key` (not a spec-compliant UUID —
/// just opaque, stable, and familiar-looking). Every id-bearing seed row uses
/// this so re-seeding an existing database overwrites in place instead of
/// accumulating duplicates.
fn det_uuid(key: &str) -> String {
    let h = det_hex(key, 32);
    format!(
        "{}-{}-{}-{}-{}",
        &h[0..8],
        &h[8..12],
        &h[12..16],
        &h[16..20],
        &h[20..32]
    )
}

fn placeholder_hlc() -> Hlc {
    // Contexts/categories are CRDT-backed: the store mints its own Hlc on
    // upsert, so this only needs to satisfy the struct shape.
    Hlc {
        wall_ms: 0,
        counter: 0,
        peer: PeerId::from("demo-seed"),
    }
}

// ─────────────────────────────── fixed cast ───────────────────────────────

struct DemoContributor {
    name: &'static str,
    login: &'static str,
    email: &'static str,
    is_agent: bool,
    agent_kind: Option<&'static str>,
}

/// Six pseudonymous identities — human-sounding names, alias handles, and
/// `@example.com` addresses (RFC 2606 reserved for documentation/testing;
/// never a real person). One is an agent identity, matching gitstate's
/// agent-native contributor model.
const CONTRIBUTORS: [DemoContributor; 6] = [
    DemoContributor {
        name: "Ada Kestrel",
        login: "ada-k",
        email: "ada.kestrel@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Femi Osei",
        login: "femi-o",
        email: "femi.osei@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Sana Torres",
        login: "sana-t",
        email: "sana.torres@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Liam Novak",
        login: "liam-n",
        email: "liam.novak@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Priya Chandran",
        login: "priya-c",
        email: "priya.chandran@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Review Agent",
        login: "gitstate-bot",
        email: "agent@example.com",
        is_agent: true,
        agent_kind: Some("ci-agent"),
    },
];

/// Five neutrally-named repos under a made-up org — no resemblance to any
/// real project intended.
const REPO_NAMES: [&str; 5] = [
    "atlas-api",
    "orbit-web",
    "ledger-core",
    "pipeline-runner",
    "docs-site",
];
const PR_COUNTS: [usize; 5] = [7, 6, 5, 6, 4];
const ISSUE_COUNTS: [usize; 5] = [4, 5, 3, 4, 3];
const COMMIT_COUNTS: [usize; 5] = [14, 12, 10, 11, 9];

const PR_TITLES: &[&str] = &[
    "Add pagination to the search endpoint",
    "Introduce retry backoff for webhook dispatch",
    "Refactor auth middleware for token rotation",
    "Speed up cold-start by lazy-loading plugins",
    "Harden input validation on the upload path",
    "Extract shared pagination helper into a crate",
    "Add dark mode to the settings panel",
    "Fix flaky integration test in CI",
    "Bump dependency set for the quarterly security batch",
    "Document the new plugin API",
    "Add rate limiting to the public API",
    "Rework the cache eviction policy",
];

const ISSUE_TITLES: &[&str] = &[
    "Search results occasionally return stale data",
    "Webhook retries can duplicate deliveries",
    "Settings panel flashes on load in dark mode",
    "Upload endpoint accepts oversized payloads",
    "CI flakes intermittently on the integration suite",
    "Docs for the plugin API are out of date",
    "Memory grows unbounded under sustained load",
    "Rate limiter under-counts burst traffic",
];

const COMMIT_SUMMARIES: &[&str] = &[
    "tighten input validation on the upload path",
    "extract shared pagination helper",
    "add regression test for retry backoff",
    "reduce allocations in the hot path",
    "fix off-by-one in cursor pagination",
    "document the plugin API surface",
    "bump transitive dependency for a CVE fix",
    "simplify auth middleware branching",
    "add dark-mode tokens to the design system",
    "guard against duplicate webhook delivery",
    "cache eviction: switch to LRU",
    "wire up the CI matrix for the new target",
];

const FILES: &[&str] = &[
    "src/api/mod.rs",
    "src/api/search.rs",
    "src/auth/middleware.rs",
    "src/cache/evict.rs",
    "web/src/routes/Settings.tsx",
    "web/src/components/Table.tsx",
    "crates/core/src/derive.rs",
    "crates/store/src/store_impl.rs",
    "docs/ARCHITECTURE.md",
    "ci/workflows/test.yml",
    "migrations/0002_index.sql",
    "src/webhook/dispatch.rs",
];

const LABELS: &[&str] = &[
    "backend",
    "frontend",
    "api",
    "performance",
    "security",
    "docs",
    "ci",
    "bug",
    "feature",
];

/// Taxonomy category keys used to give demo classifications and context tags
/// some spread across the default taxonomy (see `gitstate_core::taxonomy`).
const CATEGORY_ROTATION: &[&str] = &[
    "feature.api",
    "bugfix",
    "refactor",
    "perf",
    "docs",
    "test",
    "security",
    "chore",
    "feature.ui",
    "infra",
];

fn contributor_id(c: &DemoContributor) -> ContributorId {
    ContributorId::from(det_uuid(&format!("contributor:{}", c.email)))
}

// ────────────────────────────── the seed ──────────────────────────────

/// Write the full synthetic demo dataset into `store`. Safe to call more
/// than once against the same database: every row is keyed by a
/// deterministic id, so re-seeding overwrites in place.
pub fn seed_demo(store: &dyn Store) -> anyhow::Result<SeedSummary> {
    let mut summary = SeedSummary::default();

    let repos = seed_repos(store)?;
    summary.repos = repos.len();

    let contributors = seed_contributors(store)?;
    summary.contributors = contributors.len();

    let categories = seed_categories(store)?;
    summary.categories = categories.len();

    for (ri, repo) in repos.iter().enumerate() {
        let active: Vec<&DemoContributor> = (0..4)
            .map(|k| &CONTRIBUTORS[(ri + k) % CONTRIBUTORS.len()])
            .collect();

        let (items, counts) = build_work_items(repo, ri);
        store.save_work_items(&items)?;
        summary.work_items += items.len();

        let commits = build_commits(repo, ri, &active);
        store.save_commits(&repo.id, &commits)?;
        summary.commits += commits.len();

        let ps = build_project_state(repo, &counts);
        store.save_project_state(&ps)?;

        let contributions = build_contributions(repo, &active);
        store.save_contributions(&contributions)?;
        summary.contributions += contributions.len();

        let (classifications, effort) = build_classifications_and_effort(&items, ri);
        store.save_classifications(&classifications)?;
        store.save_effort(&effort)?;
        summary.classifications += classifications.len();
        summary.effort += effort.len();
    }

    let contexts = seed_contexts(store, &repos)?;
    summary.contexts = contexts.len();

    Ok(summary)
}

fn seed_repos(store: &dyn Store) -> anyhow::Result<Vec<Repo>> {
    let mut repos = Vec::with_capacity(REPO_NAMES.len());
    for name in REPO_NAMES {
        let repo = Repo {
            id: RepoId::from(det_uuid(&format!("repo:{name}"))),
            slug: format!("demo-org/{name}"),
            path: format!("/home/demo/code/{name}"),
            remote_url: Some(format!("https://github.com/demo-org/{name}.git")),
            forge: Forge::GitHub,
            default_branch: "main".to_string(),
            last_scanned_at: Some(days_ago(0)),
            added_at: days_ago(180),
        };
        store.upsert_repo(&repo)?;
        repos.push(repo);
    }
    Ok(repos)
}

fn seed_contributors(store: &dyn Store) -> anyhow::Result<Vec<Contributor>> {
    let mut out = Vec::with_capacity(CONTRIBUTORS.len());
    for c in &CONTRIBUTORS {
        let contributor = Contributor {
            id: contributor_id(c),
            display_name: c.name.to_string(),
            primary_email: c.email.to_string(),
            emails: vec![c.email.to_string()],
            login: Some(c.login.to_string()),
            is_agent: c.is_agent,
            agent_kind: c.agent_kind.map(str::to_string),
        };
        store.upsert_contributor(&contributor)?;
        out.push(contributor);
    }
    Ok(out)
}

fn seed_categories(store: &dyn Store) -> anyhow::Result<Vec<Category>> {
    let taxonomy: Taxonomy = Taxonomy::default_taxonomy();
    let mut out = Vec::with_capacity(taxonomy.categories.len());
    for tc in default_categories() {
        let cat = Category {
            id: CategoryId::from(det_uuid(&format!("category:{}", tc.key))),
            key: tc.key.clone(),
            label: tc.label.clone(),
            parent_key: tc.parent.clone(),
            color: tc.color.clone(),
            source: CategorySource::Taxonomy,
            taxonomy_version: Some(taxonomy.version.clone()),
            hlc: placeholder_hlc(),
            deleted: false,
        };
        store.upsert_category(&cat)?;
        out.push(cat);
    }
    Ok(out)
}

/// Aggregate counts of the work items just built, used to keep
/// [`ProjectState`] consistent with what's actually in `work_items`.
struct WorkCounts {
    open_prs: u32,
    merged_prs: u32,
    draft_prs: u32,
    open_issues: u32,
    closed_issues: u32,
}

fn build_work_items(repo: &Repo, ri: usize) -> (Vec<WorkItem>, WorkCounts) {
    let pr_count = PR_COUNTS[ri];
    let issue_count = ISSUE_COUNTS[ri];
    let mut items = Vec::with_capacity(pr_count + issue_count);
    let mut counts = WorkCounts {
        open_prs: 0,
        merged_prs: 0,
        draft_prs: 0,
        open_issues: 0,
        closed_issues: 0,
    };

    for i in 0..pr_count {
        let number = i + 1;
        let state = match i % 4 {
            0 | 1 => WorkState::Merged,
            2 => WorkState::Open,
            _ => WorkState::Draft,
        };
        match state {
            WorkState::Merged => counts.merged_prs += 1,
            WorkState::Open => counts.open_prs += 1,
            WorkState::Draft => counts.draft_prs += 1,
            _ => unreachable!(),
        }
        let created_offset = (60 - (i as i64) * 5).max(2);
        let created_at = days_ago(created_offset);
        let merged_at = matches!(state, WorkState::Merged)
            .then(|| days_ago((created_offset - 2).max(0)));
        let updated_at = merged_at.clone().unwrap_or_else(|| created_at.clone());
        let author = &CONTRIBUTORS[(ri + i) % CONTRIBUTORS.len()];
        let title = PR_TITLES[(ri * 3 + i) % PR_TITLES.len()];

        items.push(WorkItem {
            id: WorkItemId::from(det_uuid(&format!("{}:pr:{number}", repo.slug))),
            repo_id: repo.id.clone(),
            kind: WorkKind::Pr,
            external_ref: format!("#{number}"),
            title: title.to_string(),
            body: format!("Synthetic demo pull request: {title}."),
            state,
            author_login: Some(author.login.to_string()),
            labels: vec![LABELS[(ri + i) % LABELS.len()].to_string()],
            created_at,
            updated_at,
            merged_at,
            closed_at: None,
            files_touched: vec![
                FILES[(ri + i) % FILES.len()].to_string(),
                FILES[(ri + i + 3) % FILES.len()].to_string(),
            ],
        });
    }

    for j in 0..issue_count {
        let number = pr_count + j + 1;
        let state = if j % 2 == 0 {
            WorkState::Closed
        } else {
            WorkState::Open
        };
        match state {
            WorkState::Closed => counts.closed_issues += 1,
            WorkState::Open => counts.open_issues += 1,
            _ => unreachable!(),
        }
        let created_offset = (50 - (j as i64) * 6).max(1);
        let created_at = days_ago(created_offset);
        let closed_at =
            matches!(state, WorkState::Closed).then(|| days_ago((created_offset - 3).max(0)));
        let updated_at = closed_at.clone().unwrap_or_else(|| created_at.clone());
        let author = &CONTRIBUTORS[(ri + j + 1) % CONTRIBUTORS.len()];
        let title = ISSUE_TITLES[(ri * 2 + j) % ISSUE_TITLES.len()];

        items.push(WorkItem {
            id: WorkItemId::from(det_uuid(&format!("{}:issue:{number}", repo.slug))),
            repo_id: repo.id.clone(),
            kind: WorkKind::Issue,
            external_ref: format!("#{number}"),
            title: title.to_string(),
            body: format!("Synthetic demo issue: {title}."),
            state,
            author_login: Some(author.login.to_string()),
            labels: vec![LABELS[(ri + j + 2) % LABELS.len()].to_string()],
            created_at,
            updated_at,
            merged_at: None,
            closed_at,
            files_touched: vec![FILES[(ri + j + 5) % FILES.len()].to_string()],
        });
    }

    (items, counts)
}

fn build_commits(repo: &Repo, ri: usize, active: &[&DemoContributor]) -> Vec<Commit> {
    let count = COMMIT_COUNTS[ri];
    (0..count)
        .map(|k| {
            let author = active[k % active.len()];
            let key = format!("{}:commit:{k}", repo.slug);
            Commit {
                sha: det_hex(&key, 40),
                repo_id: repo.id.clone(),
                author_email: author.email.to_string(),
                author_name: author.name.to_string(),
                committed_at: days_ago((70 - (k as i64) * 4).max(1)),
                additions: det_range(&format!("{key}:add"), 5, 240) as u32,
                deletions: det_range(&format!("{key}:del"), 0, 120) as u32,
                files_changed: det_range(&format!("{key}:files"), 1, 9) as u32,
                is_merge: k % 6 == 5,
                is_test_touch: k % 3 == 0,
                summary: COMMIT_SUMMARIES[(ri + k) % COMMIT_SUMMARIES.len()].to_string(),
            }
        })
        .collect()
}

fn build_project_state(repo: &Repo, counts: &WorkCounts) -> ProjectState {
    let key = format!("{}:project-state", repo.slug);
    let p50 = det_range(&format!("{key}:p50"), 6, 36) as f64;
    ProjectState {
        repo_id: repo.id.clone(),
        head_sha: det_hex(&format!("{}:head", repo.slug), 40),
        open_prs: counts.open_prs,
        merged_prs: counts.merged_prs,
        draft_prs: counts.draft_prs,
        open_issues: counts.open_issues,
        closed_issues: counts.closed_issues,
        // Open PR ⇒ in progress; merged PR / closed issue ⇒ done (matches
        // `ProjectState`'s documented derivation).
        in_progress: counts.open_prs,
        done: counts.merged_prs + counts.closed_issues,
        cycle_time_p50_hours: Some(p50),
        cycle_time_p90_hours: Some((p50 * 2.2 * 10.0).round() / 10.0),
        change_failure_rate: Some(det_range(&format!("{key}:cfr"), 2, 18) as f64 / 100.0),
        computed_at: days_ago(0),
        warnings: vec!["synthetic demo data — not derived from real git/forge history".into()],
    }
}

fn build_contributions(repo: &Repo, active: &[&DemoContributor]) -> Vec<Contribution> {
    active
        .iter()
        .map(|c| {
            let base = format!("{}:{}", repo.slug, c.login);
            let dims = Dimensions {
                shipped: det_range(&format!("{base}:shipped"), 15, 97) as f64,
                review: det_range(&format!("{base}:review"), 10, 95) as f64,
                effort: det_range(&format!("{base}:effort"), 20, 98) as f64,
                quality: det_range(&format!("{base}:quality"), 25, 96) as f64,
                ownership: det_range(&format!("{base}:ownership"), 5, 90) as f64,
                durability: det_range(&format!("{base}:durability"), 30, 99) as f64,
            };
            let human_commits = if c.is_agent {
                det_range(&format!("{base}:human_commits"), 2, 10) as u32
            } else {
                det_range(&format!("{base}:human_commits"), 5, 40) as u32
            };
            let agent_commits = if c.is_agent {
                det_range(&format!("{base}:agent_commits"), 15, 60) as u32
            } else {
                0
            };
            let surviving_lines = det_range(&format!("{base}:surviving"), 200, 4000) as u32;
            let raw = DimensionRaw {
                merged_prs: det_range(&format!("{base}:raw_merged"), 0, 9) as u32,
                closed_issues: det_range(&format!("{base}:raw_closed"), 0, 6) as u32,
                reviews_done: det_range(&format!("{base}:raw_reviews"), 0, 12) as u32,
                effort_points: det_range(&format!("{base}:raw_effort"), 10, 120) as f64 / 2.0,
                reverts_caused: det_range(&format!("{base}:raw_reverts"), 0, 2) as u32,
                bug_intros: det_range(&format!("{base}:raw_bugs"), 0, 3) as u32,
                areas_owned: det_range(&format!("{base}:raw_areas"), 1, 6) as u32,
                surviving_lines,
                authored_lines: surviving_lines
                    + det_range(&format!("{base}:raw_added"), 50, 800) as u32,
                human_commits,
                agent_commits,
            };
            let agent_pct = agent_commits as f64 / (human_commits + agent_commits).max(1) as f64;
            let composite =
                (dims.shipped + dims.review + dims.effort + dims.quality + dims.ownership
                    + dims.durability)
                    / 6.0;
            Contribution {
                contributor_id: contributor_id(c),
                repo_id: repo.id.clone(),
                from: WINDOW_FROM.to_string(),
                to: WINDOW_TO.to_string(),
                dimensions: dims,
                raw,
                agent_pct,
                composite,
            }
        })
        .collect()
}

fn build_classifications_and_effort(
    items: &[WorkItem],
    ri: usize,
) -> (Vec<Classification>, Vec<EffortEstimate>) {
    // Classify/estimate a handful of items per repo — enough to populate the
    // classify/effort surfaces without pretending every item was judged.
    let sample: Vec<&WorkItem> = items.iter().take(3).collect();
    let mut classifications = Vec::with_capacity(sample.len());
    let mut effort = Vec::with_capacity(sample.len());
    for (idx, item) in sample.iter().enumerate() {
        let key = format!("{}:{}", item.repo_id.0, item.external_ref);
        let category_key = CATEGORY_ROTATION[(ri * 3 + idx) % CATEGORY_ROTATION.len()];
        classifications.push(Classification {
            item_id: item.id.clone(),
            category_key: category_key.to_string(),
            confidence: det_range(&format!("{key}:conf"), 60, 96) as f64 / 100.0,
            method: EffortMethod::Heuristic,
            rationale: format!(
                "Synthetic demo classification for \"{}\" (title/labels heuristic).",
                item.title
            ),
        });
        effort.push(EffortEstimate {
            item_id: item.id.clone(),
            difficulty: det_range(&format!("{key}:difficulty"), 1, 13) as f64,
            method: EffortMethod::Heuristic,
            rationale: format!("Synthetic demo effort estimate for \"{}\".", item.title),
            confidence: det_range(&format!("{key}:effort_conf"), 55, 92) as f64 / 100.0,
        });
    }
    (classifications, effort)
}

fn seed_contexts(store: &dyn Store, repos: &[Repo]) -> anyhow::Result<Vec<Context>> {
    let repo_id = |name: &str| -> RepoId {
        repos
            .iter()
            .find(|r| r.slug.ends_with(name))
            .expect("demo repo exists")
            .id
            .clone()
    };

    let contexts = vec![
        Context {
            id: ContextId::from(det_uuid("context:launch-push")),
            name: "Launch push".to_string(),
            description: "Repos and PRs tracked for the current release window.".to_string(),
            repo_ids: vec![repo_id("atlas-api"), repo_id("orbit-web")],
            pr_refs: vec![
                ContextPrRef {
                    repo_slug: "demo-org/atlas-api".to_string(),
                    number: 1,
                    note: Some("ship blocker".to_string()),
                },
                ContextPrRef {
                    repo_slug: "demo-org/orbit-web".to_string(),
                    number: 3,
                    note: None,
                },
            ],
            notes: "Synthetic demo context: coordinating the API + web launch surface."
                .to_string(),
            tags: vec!["feature.api".to_string(), "launch".to_string()],
            created_at: days_ago(21),
            updated_at: days_ago(1),
            hlc: placeholder_hlc(),
            deleted: false,
        },
        Context {
            id: ContextId::from(det_uuid("context:reliability-sweep")),
            name: "Reliability sweep".to_string(),
            description: "Bug fixes and perf work queued for the reliability pass."
                .to_string(),
            repo_ids: vec![repo_id("ledger-core"), repo_id("pipeline-runner")],
            pr_refs: vec![ContextPrRef {
                repo_slug: "demo-org/ledger-core".to_string(),
                number: 2,
                note: Some("regression from last release".to_string()),
            }],
            notes: "Synthetic demo context: tracking flaky/duplicate-delivery fixes."
                .to_string(),
            tags: vec!["bugfix".to_string(), "perf".to_string()],
            created_at: days_ago(14),
            updated_at: days_ago(2),
            hlc: placeholder_hlc(),
            deleted: false,
        },
    ];

    for c in &contexts {
        store.upsert_context(c)?;
    }
    Ok(contexts)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn seeds_a_nonempty_demo_dataset_deterministically() {
        let store = SqliteStore::open_in_memory().expect("open in-memory store");
        let summary = seed_demo(&store).expect("seed demo");

        assert_eq!(summary.repos, REPO_NAMES.len());
        assert_eq!(summary.contributors, CONTRIBUTORS.len());
        assert_eq!(summary.categories, default_categories().len());
        assert!(summary.work_items > 0);
        assert!(summary.commits > 0);
        assert!(summary.contributions > 0);
        assert!(summary.classifications > 0);
        assert!(summary.effort > 0);
        assert_eq!(summary.contexts, 2);

        // Every endpoint the daemon exposes for a demo screenshot must have
        // rows: repos, project-state, contributions, work-items, contexts,
        // categories, contributors.
        let repos = store.list_repos().expect("list repos");
        assert_eq!(repos.len(), REPO_NAMES.len());
        for r in &repos {
            assert!(store
                .get_project_state(&r.id)
                .expect("project state")
                .is_some());
            assert!(!store
                .get_contributions(&r.id, WINDOW_FROM, WINDOW_TO)
                .expect("contributions")
                .is_empty());
            assert!(!store.list_work_items(&r.id).expect("work items").is_empty());
        }
        assert_eq!(
            store.list_contributors().expect("contributors").len(),
            CONTRIBUTORS.len()
        );
        assert_eq!(store.list_contexts().expect("contexts").len(), 2);
        assert!(store.list_categories().expect("categories").len() >= default_categories().len());

        // Re-seeding is idempotent: no duplicate rows accumulate.
        seed_demo(&store).expect("reseed demo");
        assert_eq!(store.list_repos().expect("list repos").len(), REPO_NAMES.len());
        assert_eq!(
            store.list_contributors().expect("contributors").len(),
            CONTRIBUTORS.len()
        );
        assert_eq!(store.list_contexts().expect("contexts").len(), 2);
    }
}
