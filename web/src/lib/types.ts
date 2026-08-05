/**
 * Wire types for the gitstate daemon's JSON API.
 *
 * These mirror the Rust shapes in `crates/gitstate-core` and
 * `crates/gitstate-daemon` field-for-field (see `domain.rs`, `analytics.rs`,
 * `health.rs`, `taxonomy.rs`, `dto.rs`). All IDs are `#[serde(transparent)]`
 * newtypes, so they cross the wire as bare strings. Rust `Option<T>` fields
 * serialize as JSON `null` (not an omitted key) unless noted otherwise, so
 * those are typed `T | null` rather than optional.
 */

// ── Small shared aliases ────────────────────────────────────────────────────

export type RepoId = string
export type ContributorId = string
export type WorkItemId = string
export type CategoryId = string
export type ContextId = string
export type PeerId = string

export interface Hlc {
  wall_ms: number
  counter: number
  peer: PeerId
}

// ── Repos ────────────────────────────────────────────────────────────────────

export type Forge = 'github' | 'gitlab' | 'local'

export interface Repo {
  id: RepoId
  slug: string
  path: string
  remote_url: string | null
  forge: Forge
  default_branch: string
  last_scanned_at: string | null
  added_at: string
}

export interface Contributor {
  id: ContributorId
  display_name: string
  primary_email: string
  emails: string[]
  login: string | null
  is_agent: boolean
  agent_kind: string | null
}

// ── Work items ───────────────────────────────────────────────────────────────

export type WorkKind = 'commit' | 'pr' | 'issue' | 'review'

export type WorkState = 'open' | 'in_progress' | 'merged' | 'closed' | 'done' | 'draft'

export interface WorkItem {
  id: WorkItemId
  repo_id: RepoId
  kind: WorkKind
  external_ref: string
  title: string
  body: string
  state: WorkState
  author_login: string | null
  labels: string[]
  created_at: string
  updated_at: string
  merged_at: string | null
  closed_at: string | null
  files_touched: string[]
}

// ── Project state ────────────────────────────────────────────────────────────

export interface ProjectState {
  repo_id: RepoId
  head_sha: string
  open_prs: number
  merged_prs: number
  draft_prs: number
  open_issues: number
  closed_issues: number
  in_progress: number
  done: number
  cycle_time_p50_hours: number | null
  cycle_time_p90_hours: number | null
  change_failure_rate: number | null
  computed_at: string
  warnings: string[]
}

// ── Contribution (six dimensions) ───────────────────────────────────────────

export interface Dimensions {
  shipped: number
  review: number
  effort: number
  quality: number
  ownership: number
  durability: number
}

export interface DimensionRaw {
  merged_prs: number
  closed_issues: number
  reviews_done: number
  effort_points: number
  reverts_caused: number
  bug_intros: number
  areas_owned: number
  surviving_lines: number
  authored_lines: number
  human_commits: number
  agent_commits: number
}

export interface Contribution {
  contributor_id: ContributorId
  repo_id: RepoId
  from: string
  to: string
  dimensions: Dimensions
  raw: DimensionRaw
  agent_pct: number
  composite: number
}

export interface Weights {
  shipped: number
  review: number
  effort: number
  quality: number
  ownership: number
  durability: number
}

/** One contributor's six dimensions merged across every repo — `GET /api/contributions/rollup`. */
export interface ContributionRollupRow {
  contributor_id: ContributorId
  display_name: string
  primary_email: string
  is_agent: boolean
  dimensions: Dimensions
  raw: DimensionRaw
  agent_pct: number
  composite: number
  repos: string[]
}

// ── Classification + effort ─────────────────────────────────────────────────

export type EffortMethod = 'llm_judged' | 'heuristic'

export interface EffortEstimate {
  item_id: WorkItemId
  difficulty: number
  method: EffortMethod
  rationale: string
  confidence: number
}

export interface Classification {
  item_id: WorkItemId
  category_key: string
  confidence: number
  method: EffortMethod
  rationale: string
}

// ── Categories ───────────────────────────────────────────────────────────────

export type CategorySource = 'taxonomy' | 'local' | 'peer'

export interface Category {
  id: CategoryId
  key: string
  label: string
  parent_key: string | null
  color: string | null
  source: CategorySource
  taxonomy_version: string | null
  hlc: Hlc
  deleted: boolean
}

// ── Contexts (saved working sets) ───────────────────────────────────────────

export interface ContextPrRef {
  repo_slug: string
  number: number
  note: string | null
}

export interface Context {
  id: ContextId
  name: string
  description: string
  repo_ids: RepoId[]
  pr_refs: ContextPrRef[]
  notes: string
  tags: string[]
  created_at: string
  updated_at: string
  hlc: Hlc
  deleted: boolean
}

/** `POST /api/contexts` / `PATCH /api/contexts/:id` body — every field optional. */
export interface ContextInput {
  name?: string
  description?: string
  repo_ids?: RepoId[]
  pr_refs?: ContextPrRef[]
  notes?: string
  tags?: string[]
}

/** `POST /api/categories` body. */
export interface CategoryInput {
  key: string
  label: string
  parent_key?: string
  color?: string
}

// ── Analytics (`GET /api/analytics`) ────────────────────────────────────────

export interface AnalyticsRange {
  from: string
  to: string
  days: number
}

export interface DayBucket {
  date: string
  /** 0 = Monday … 6 = Sunday. */
  weekday: number
  commits: number
  additions: number
  deletions: number
}

export interface WeekBucket {
  week_start: string
  commits: number
  additions: number
  deletions: number
  complete: boolean
}

export interface ContributorStat {
  email: string
  name: string
  commits: number
  additions: number
  deletions: number
  files_changed: number
  active_days: number
  is_agent: boolean
}

export interface CyclePoint {
  merged_at: string
  hours: number
  external_ref: string
  title: string
}

export interface ThroughputPoint {
  week_start: string
  merged_prs: number
  closed_issues: number
}

export interface Slice {
  key: string
  count: number
}

export interface Totals {
  commits: number
  repos: number
  contributors: number
  additions: number
  deletions: number
  net_lines: number
  active_days: number
  merge_commits: number
  test_touch_commits: number
  open_prs: number
  merged_prs: number
  open_issues: number
  closed_issues: number
  commits_per_active_day: number
  lines_per_commit: number
  test_touch_rate: number
  cycle_p50_hours: number | null
  cycle_p90_hours: number | null
}

export interface Analytics {
  range: AnalyticsRange
  totals: Totals
  heatmap: DayBucket[]
  weekly: WeekBucket[]
  contributors: ContributorStat[]
  cycle_time: CyclePoint[]
  throughput: ThroughputPoint[]
  work_kinds: Slice[]
  work_states: Slice[]
  labels: Slice[]
}

// ── Engineering health + involvement (`/api/health-metrics`, `/api/involvement`) ──

export interface Dora {
  cycle_p50_hours: number | null
  cycle_p90_hours: number | null
  change_failure_rate: number | null
  merge_frequency_per_week: number
  deploy_proxy_per_week: number
  lead_time_samples: number
}

export interface OwnershipShare {
  email: string
  name: string
  commits: number
  share: number
  is_agent: boolean
}

export interface BusFactor {
  count: number
  top_share: number
  contributors: OwnershipShare[]
}

export interface ReviewHealth {
  merged_prs: number
  reviews_done: number
  reviewed_pr_share: number
  unreviewed_merged: number
}

export interface Quality {
  test_touch_rate: number
  avg_commit_size_lines: number
  large_commit_share: number
  revert_commits: number
}

export interface EngHealth {
  range: AnalyticsRange
  dora: Dora
  bus_factor: BusFactor
  review: ReviewHealth
  quality: Quality
}

export interface PersonRepoShare {
  repo_id: RepoId
  slug: string
  commits: number
  share: number
}

export interface PersonInvolvement {
  email: string
  name: string
  is_agent: boolean
  total_commits: number
  repo_count: number
  repos: PersonRepoShare[]
}

export interface RepoInvolvement {
  repo_id: RepoId
  slug: string
  commits: number
  contributors: OwnershipShare[]
}

export interface Involvement {
  repos: RepoInvolvement[]
  people: PersonInvolvement[]
}

// ── Taxonomy ─────────────────────────────────────────────────────────────────

export interface TaxonomyCategory {
  key: string
  label: string
  parent: string | null
  color: string | null
  description: string
}

export interface Taxonomy {
  schema: string
  version: string
  id: string
  issued_at: string
  categories: TaxonomyCategory[]
  pubkey: string
  sig: string
}

export interface VerifyResp {
  valid: boolean
  id: string
}

// ── Sync ─────────────────────────────────────────────────────────────────────

export interface SyncStatus {
  enabled: boolean
  peer_id: PeerId
  peers: number
  last_op_hlc: Hlc | null
}

// ── Trackers (Jira / Linear) + import ───────────────────────────────────────

export type TrackerKind = 'jira' | 'linear'

export interface TrackerView {
  kind: TrackerKind
  configured: boolean
  base_url: string
  email: string
  project: string
  /** Masked (`…9f2c`), never the real secret. */
  token: string
}

export interface TrackerStatus {
  ok: boolean
  account: string | null
  message: string
}

export interface ImportedItem {
  source: string
  key: string
  title: string
  body: string
  state: WorkState
  author: string | null
  labels: string[]
  created_at: string
  updated_at: string
  closed_at: string | null
  url: string | null
}

export interface PreviewResp {
  items: ImportedItem[]
  count: number
}

export interface ImportResp {
  imported: number
  repo_id: RepoId
}

// ── Repo mutation results ───────────────────────────────────────────────────

export interface ScanResult {
  repo_id: RepoId
  head_sha: string
  commits_scanned: number
  contributors: number
  work_items: number
  project_state: ProjectState
  warnings: string[]
}

export interface DeletedResp {
  deleted: boolean
}

// ── Health / meta ───────────────────────────────────────────────────────────

export interface HealthResp {
  status: string
  version: string
  sync: boolean
  classifier: string
}

// ── Agent runs (the AI-agent write path) ────────────────────────────────────

export type HumanAction = 'accepted' | 'edited' | 'reverted'

export interface AgentDiffSummary {
  additions: number
  deletions: number
  changed_files: number
}

export interface AgentRun {
  id: string
  repo_id: string | null
  pr_id: string | null
  issue_id: string | null
  supervisor_id: string | null
  goal: string
  agent_name: string | null
  branch: string | null
  diff_summary: AgentDiffSummary
  tests_passed: boolean | null
  human_action: HumanAction | null
  iterations: number | null
  cost_usd: number | null
  created_at: string
}
