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

/// A timestamp `days` back from the anchor at an explicit wall-clock time.
/// Commits need intra-day spread (a heatmap day with six commits should not
/// stamp all six at 09:00), which `days_ago` alone can't express.
fn at_day(days: i64, hour: u8, minute: u8) -> String {
    (anchor() - Duration::days(days.max(0)))
        .date()
        .with_hms(hour.min(23), minute.min(59), 0)
        .expect("valid time of day")
        .assume_utc()
        .format(&Rfc3339)
        .expect("format rfc3339")
}

/// 0 = Monday … 6 = Sunday for the day `days` back from the anchor.
fn weekday_of(days: i64) -> u8 {
    (anchor() - Duration::days(days.max(0)))
        .date()
        .weekday()
        .number_days_from_monday()
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

/// Ten pseudonymous identities — human-sounding names, alias handles, and
/// `@example.com` addresses (RFC 2606 reserved for documentation/testing;
/// never a real person). Two are agent identities, matching gitstate's
/// agent-native contributor model. A cast this size gives the contributor
/// leaderboard a believable long tail instead of four near-equal bars.
const CONTRIBUTORS: [DemoContributor; 10] = [
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
        name: "Mateo Ruiz",
        login: "mateo-r",
        email: "mateo.ruiz@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Nour Haddad",
        login: "nour-h",
        email: "nour.haddad@example.com",
        is_agent: false,
        agent_kind: None,
    },
    DemoContributor {
        name: "Wei Zhang",
        login: "wei-z",
        email: "wei.zhang@example.com",
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
    DemoContributor {
        name: "Refactor Agent",
        login: "refactor-bot",
        email: "refactor-agent@example.com",
        is_agent: true,
        agent_kind: Some("coding-agent"),
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
/// How far back the synthetic history runs. 53 weeks so the "1y" range renders
/// a full contribution heatmap with no dead leading months, and the trend
/// charts have enough points to have shape rather than reading as three dots
/// joined by a line.
const HISTORY_DAYS: i64 = 371;

/// Per-repo activity multiplier, so the five repos don't all look identical:
/// `atlas-api` is the busy monorepo-ish core, `docs-site` ticks over quietly.
const REPO_INTENSITY: [f64; 5] = [1.0, 0.82, 0.66, 0.54, 0.3];

const PR_COUNTS: [usize; 5] = [46, 38, 30, 26, 16];
const ISSUE_COUNTS: [usize; 5] = [28, 24, 18, 16, 11];

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
    "Migrate the job queue off the legacy scheduler",
    "Add structured logging to the request path",
    "Split the settings store into typed sections",
    "Backfill missing indexes on the events table",
    "Replace the hand-rolled CSV parser",
    "Add an idempotency key to webhook delivery",
    "Tighten the CSP on the embedded dashboard",
    "Cut cold-path allocations in the serializer",
    "Add a health endpoint for the worker pool",
    "Support cursor-based pagination on exports",
    "Deduplicate the retry and backoff helpers",
    "Add golden-file tests for the report renderer",
    "Move secrets loading behind a provider trait",
    "Trim the release image by half",
    "Make the migration runner resumable",
    "Add OpenTelemetry spans to the ingest path",
    "Fix timezone drift in the weekly rollup",
    "Batch the notification fan-out",
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
    "Export job times out on large accounts",
    "Weekly rollup is off by one day near DST",
    "Worker pool deadlocks when the queue drains",
    "Migration runner cannot resume after a crash",
    "Report renderer drops the last table row",
    "Notification fan-out sends duplicates on retry",
    "Cursor pagination skips items on concurrent writes",
    "Cold start regressed after the plugin change",
    "CSV import mangles quoted newlines",
    "Health endpoint reports ready before warmup",
    "Structured logs lose the trace id on retry",
    "Index bloat on the events table slows queries",
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
    "drop the unused legacy scheduler shim",
    "add structured logging to the request path",
    "backfill the missing events index",
    "make the migration runner resumable",
    "batch notification fan-out by recipient",
    "add golden-file test for the report renderer",
    "fix timezone drift in the weekly rollup",
    "trim the release image layers",
    "thread the trace id through retries",
    "handle quoted newlines in the CSV import",
    "split the settings store into typed sections",
    "add an idempotency key to webhook delivery",
    "warm the worker pool before reporting ready",
    "replace the hand-rolled parser with serde",
    "tighten the embedded dashboard CSP",
    "cut allocations in the serializer cold path",
    "support cursor pagination on exports",
    "add OpenTelemetry spans to ingest",
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
        // Six of the ten-person cast touch each repo, offset per repo so the
        // whole cast appears across the org while each repo keeps its own
        // recognizable core of owners.
        let active: Vec<&DemoContributor> = (0..6)
            .map(|k| &CONTRIBUTORS[(ri * 2 + k) % CONTRIBUTORS.len()])
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
            // Added when the history starts, so "tracked since" lines up with
            // the first lit cell in the heatmap.
            added_at: days_ago(HISTORY_DAYS),
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

    // PRs march backward from the anchor across the whole history window, so
    // the cycle-time trend and weekly throughput series both span it.
    let pr_stride = (HISTORY_DAYS - 6) as f64 / pr_count.max(1) as f64;

    for i in 0..pr_count {
        let number = i + 1;
        let key = format!("{}:pr:{number}", repo.slug);
        // The newest few PRs are still in flight; everything older has landed.
        let state = if i < 2 {
            WorkState::Draft
        } else if i < 6 {
            WorkState::Open
        } else {
            WorkState::Merged
        };
        match state {
            WorkState::Merged => counts.merged_prs += 1,
            WorkState::Open => counts.open_prs += 1,
            WorkState::Draft => counts.draft_prs += 1,
            _ => unreachable!(),
        }

        let created_offset = (2.0 + i as f64 * pr_stride).round() as i64;
        let created_at = at_day(
            created_offset,
            det_range(&format!("{key}:h"), 9, 17) as u8,
            det_range(&format!("{key}:m"), 0, 59) as u8,
        );

        // Lead time: mostly hours-to-days, with a long tail of stragglers —
        // the shape that makes a p50/p90 split worth showing at all.
        let lead_hours = match det_u64(&format!("{key}:lead")) % 100 {
            0..=44 => det_range(&format!("{key}:lead-fast"), 2, 30),
            45..=79 => det_range(&format!("{key}:lead-mid"), 30, 96),
            80..=94 => det_range(&format!("{key}:lead-slow"), 96, 336),
            _ => det_range(&format!("{key}:lead-stuck"), 336, 900),
        } as i64;
        let merged_at = matches!(state, WorkState::Merged).then(|| {
            (anchor() - Duration::days(created_offset) + Duration::hours(lead_hours))
                .format(&Rfc3339)
                .expect("format rfc3339")
        });
        let updated_at = merged_at.clone().unwrap_or_else(|| created_at.clone());
        let author =
            &CONTRIBUTORS[(det_u64(&format!("{key}:author")) as usize) % CONTRIBUTORS.len()];
        let title = PR_TITLES[(ri * 3 + i) % PR_TITLES.len()];

        items.push(WorkItem {
            id: WorkItemId::from(det_uuid(&key)),
            repo_id: repo.id.clone(),
            kind: WorkKind::Pr,
            external_ref: format!("#{number}"),
            title: title.to_string(),
            body: format!("Synthetic demo pull request: {title}."),
            state,
            author_login: Some(author.login.to_string()),
            labels: vec![
                LABELS[(det_u64(&format!("{key}:l1")) as usize) % LABELS.len()].to_string(),
                LABELS[(det_u64(&format!("{key}:l2")) as usize) % LABELS.len()].to_string(),
            ],
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

    let issue_stride = (HISTORY_DAYS - 4) as f64 / issue_count.max(1) as f64;

    for j in 0..issue_count {
        let number = pr_count + j + 1;
        let key = format!("{}:issue:{number}", repo.slug);
        // Recent issues are still open; the backlog behind them got closed.
        let state = if j < 5 {
            WorkState::Open
        } else if det_u64(&format!("{key}:state")) % 10 < 8 {
            WorkState::Closed
        } else {
            WorkState::Open
        };
        match state {
            WorkState::Closed => counts.closed_issues += 1,
            WorkState::Open => counts.open_issues += 1,
            _ => unreachable!(),
        }
        let created_offset = (1.0 + j as f64 * issue_stride).round() as i64;
        let created_at = days_ago(created_offset);
        let closed_at = matches!(state, WorkState::Closed).then(|| {
            let age = det_range(&format!("{key}:age"), 1, 40) as i64;
            days_ago((created_offset - age).max(0))
        });
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

/// How many commits a repo lands on the day `days_back` from the anchor.
///
/// Deterministic but shaped like a real team's calendar so the contribution
/// heatmap has texture rather than uniform noise: weekdays carry the work,
/// weekends are mostly silent with the occasional push, a long tail of quiet
/// days sits next to occasional bursts, and the whole window ramps up toward
/// the present (the team grew). `intensity` scales the repo overall.
fn commits_on_day(repo_slug: &str, days_back: i64, intensity: f64) -> u32 {
    let weekday = weekday_of(days_back);
    let roll = det_u64(&format!("{repo_slug}:day:{days_back}")) % 100;

    if weekday >= 5 {
        // Weekend: usually nothing, sometimes a small push.
        let base = match roll {
            0..=73 => 0,
            74..=93 => 1,
            _ => 2,
        };
        return (base as f64 * intensity).round() as u32;
    }

    // `days_back` counts backward, so invert it into a 0..1 recency ramp.
    let recency = (HISTORY_DAYS - days_back).max(0) as f64 / HISTORY_DAYS as f64;
    let ramp = 0.5 + 0.5 * recency;
    let base = match roll {
        0..=10 => 0, // even weekdays go quiet sometimes
        11..=39 => 2,
        40..=68 => 4,
        69..=86 => 6,
        87..=96 => 9,
        _ => 14, // a release-day burst
    };
    (base as f64 * ramp * intensity).round() as u32
}

/// Walk every day in the history window and emit that day's commits. Authors
/// rotate deterministically through the repo's active cast, weighted so the
/// leaderboard has a clear top few and a believable tail.
fn build_commits(repo: &Repo, ri: usize, active: &[&DemoContributor]) -> Vec<Commit> {
    let intensity = REPO_INTENSITY[ri];
    let mut out = Vec::new();

    for days_back in (0..HISTORY_DAYS).rev() {
        let n = commits_on_day(&repo.slug, days_back, intensity);
        for k in 0..n {
            let key = format!("{}:commit:{days_back}:{k}", repo.slug);

            // Weighted author pick: squaring a uniform roll biases toward the
            // front of the cast, which is what a real repo's blame looks like.
            let roll = det_u64(&format!("{key}:author")) % 1000;
            let frac = roll as f64 / 1000.0;
            let idx = ((frac * frac) * active.len() as f64) as usize;
            let author = active[idx.min(active.len() - 1)];

            // Spread across a working day, with a little evening tail.
            let hour = det_range(&format!("{key}:hour"), 8, 20) as u8;
            let minute = det_range(&format!("{key}:min"), 0, 59) as u8;

            // Most commits are small; a few are large. A flat 5..240 range made
            // every commit look like a rewrite.
            let size_roll = det_u64(&format!("{key}:size")) % 100;
            let (lo, hi) = match size_roll {
                0..=59 => (1, 40),
                60..=87 => (40, 180),
                88..=97 => (180, 600),
                _ => (600, 2400),
            };
            let additions = det_range(&format!("{key}:add"), lo, hi) as u32;
            let deletions = det_range(&format!("{key}:del"), 0, (hi / 2).max(1)) as u32;

            out.push(Commit {
                sha: det_hex(&key, 40),
                repo_id: repo.id.clone(),
                author_email: author.email.to_string(),
                author_name: author.name.to_string(),
                committed_at: at_day(days_back, hour, minute),
                additions,
                deletions,
                files_changed: det_range(&format!("{key}:files"), 1, 14) as u32,
                is_merge: det_u64(&format!("{key}:merge")) % 9 == 0,
                is_test_touch: det_u64(&format!("{key}:test")) % 100 < 38,
                summary: COMMIT_SUMMARIES
                    [(det_u64(&format!("{key}:msg")) as usize) % COMMIT_SUMMARIES.len()]
                .to_string(),
            });
        }
    }
    out
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
            let composite = (dims.shipped
                + dims.review
                + dims.effort
                + dims.quality
                + dims.ownership
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
    // Classify/estimate most items, leaving a visible unclassified remainder so
    // the Classify screen still has something to act on.
    let sample: Vec<&WorkItem> = items
        .iter()
        .filter(|w| det_u64(&format!("{}:{}:judged", w.repo_id.0, w.external_ref)) % 10 < 8)
        .collect();
    let mut classifications = Vec::with_capacity(sample.len());
    let mut effort = Vec::with_capacity(sample.len());
    for (idx, item) in sample.iter().enumerate() {
        let key = format!("{}:{}", item.repo_id.0, item.external_ref);
        // Spread across the taxonomy by content hash rather than position, so
        // the category breakdown isn't a perfectly even round-robin.
        let category_key = CATEGORY_ROTATION
            [(det_u64(&format!("{key}:cat")) as usize + ri + idx) % CATEGORY_ROTATION.len()];
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
            notes: "Synthetic demo context: coordinating the API + web launch surface.".to_string(),
            tags: vec!["feature.api".to_string(), "launch".to_string()],
            created_at: days_ago(21),
            updated_at: days_ago(1),
            hlc: placeholder_hlc(),
            deleted: false,
        },
        Context {
            id: ContextId::from(det_uuid("context:reliability-sweep")),
            name: "Reliability sweep".to_string(),
            description: "Bug fixes and perf work queued for the reliability pass.".to_string(),
            repo_ids: vec![repo_id("ledger-core"), repo_id("pipeline-runner")],
            pr_refs: vec![ContextPrRef {
                repo_slug: "demo-org/ledger-core".to_string(),
                number: 2,
                note: Some("regression from last release".to_string()),
            }],
            notes: "Synthetic demo context: tracking flaky/duplicate-delivery fixes.".to_string(),
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
        assert_eq!(
            store.list_repos().expect("list repos").len(),
            REPO_NAMES.len()
        );
        assert_eq!(
            store.list_contributors().expect("contributors").len(),
            CONTRIBUTORS.len()
        );
        assert_eq!(store.list_contexts().expect("contexts").len(), 2);
    }

    /// The demo dataset exists to make the visualizations look like a real
    /// team's ledger. These assertions pin the properties the heatmap, trend
    /// charts and leaderboard actually depend on — thin data is the exact
    /// regression this guards against.
    #[test]
    fn demo_dataset_is_dense_enough_to_visualize() {
        use gitstate_core::analytics;

        let store = SqliteStore::open_in_memory().expect("open in-memory store");
        seed_demo(&store).expect("seed demo");

        let commits = store.list_commits(None).expect("commits");
        let items = store.list_all_work_items().expect("work items");
        let known = store.list_contributors().expect("contributors");

        assert!(
            commits.len() > 1_000,
            "a heatmap needs volume; got {} commits",
            commits.len()
        );

        let (from, to) = analytics::range_ending(DEMO_ANCHOR, HISTORY_DAYS as u32).unwrap();
        let a = analytics::compute(&commits, &items, &known, 5, &from, &to);

        // Dense grid, and genuinely busy — not a handful of lit cells.
        assert_eq!(a.heatmap.len(), HISTORY_DAYS as usize);
        assert!(
            a.totals.active_days > 150,
            "heatmap should be mostly lit; {} active days",
            a.totals.active_days
        );

        // Weekends visibly quieter than weekdays — the texture that makes a
        // contribution heatmap read as real.
        let weekday_commits: u32 = a
            .heatmap
            .iter()
            .filter(|d| d.weekday < 5)
            .map(|d| d.commits)
            .sum();
        let weekend_commits: u32 = a
            .heatmap
            .iter()
            .filter(|d| d.weekday >= 5)
            .map(|d| d.commits)
            .sum();
        assert!(
            weekday_commits > weekend_commits * 5,
            "weekday {weekday_commits} vs weekend {weekend_commits}"
        );

        // A leaderboard with a real distribution, not four equal bars.
        assert!(
            a.contributors.len() >= 8,
            "got {} contributors",
            a.contributors.len()
        );
        let top = a.contributors[0].commits;
        let last = a.contributors[a.contributors.len() - 1].commits;
        assert!(
            top > last * 2,
            "leaderboard should have a spread: {top} vs {last}"
        );

        // Trend charts need enough points to have shape.
        assert!(
            a.cycle_time.len() > 100,
            "cycle-time trend has {} points",
            a.cycle_time.len()
        );
        assert!(
            a.weekly.len() >= 40,
            "weekly series has {} points",
            a.weekly.len()
        );
        assert!(
            a.throughput.len() >= 20,
            "throughput series has {} points",
            a.throughput.len()
        );

        // p90 above p50 — the split is only worth rendering if it's real.
        let p50 = a.totals.cycle_p50_hours.expect("p50");
        let p90 = a.totals.cycle_p90_hours.expect("p90");
        assert!(p90 > p50, "p90 {p90} should exceed p50 {p50}");

        // Categorical breakdowns have several slices to colour.
        assert!(a.labels.len() >= 5, "labels: {:?}", a.labels);
        assert!(a.totals.additions > 0 && a.totals.deletions > 0);
    }

    #[test]
    fn commit_generation_is_deterministic_across_runs() {
        let a = SqliteStore::open_in_memory().unwrap();
        let b = SqliteStore::open_in_memory().unwrap();
        seed_demo(&a).unwrap();
        seed_demo(&b).unwrap();

        let ca = a.list_commits(None).unwrap();
        let cb = b.list_commits(None).unwrap();
        assert_eq!(ca.len(), cb.len());
        // Same shas, same timestamps, same authors — screenshots stay stable.
        for (x, y) in ca.iter().zip(cb.iter()) {
            assert_eq!(x.sha, y.sha);
            assert_eq!(x.committed_at, y.committed_at);
            assert_eq!(x.author_email, y.author_email);
            assert_eq!(x.additions, y.additions);
        }
    }

    #[test]
    fn weekends_are_quiet_but_not_dead() {
        // The shaping function is the thing the heatmap's texture rests on.
        let weekday_total: u32 = (0..HISTORY_DAYS)
            .filter(|d| weekday_of(*d) < 5)
            .map(|d| commits_on_day("demo-org/atlas-api", d, 1.0))
            .sum();
        let weekend_total: u32 = (0..HISTORY_DAYS)
            .filter(|d| weekday_of(*d) >= 5)
            .map(|d| commits_on_day("demo-org/atlas-api", d, 1.0))
            .sum();
        assert!(weekend_total > 0, "an occasional weekend push is expected");
        assert!(weekday_total > weekend_total * 5);
    }
}
