/**
 * gitstate API client (local-first).
 *
 * Talks to the gitstate daemon (`gitstated`) — the same axum server whether the
 * app runs headless (daemon serves the SPA same-origin) or inside the Tauri
 * shell (daemon on an ephemeral 127.0.0.1 port, injected before first paint).
 *
 * There is NO auth, NO org scoping, NO tokens — this is a single-user local app.
 * Every network call in the app funnels through this module; no component calls
 * `fetch` directly.
 */
import type {
  AgentDiffSummary,
  AgentRun,
  Analytics,
  Category,
  CategoryInput,
  Classification,
  Context,
  ContextInput,
  ContributionRollupRow,
  Contribution,
  Contributor,
  DeletedResp,
  EffortEstimate,
  EngHealth,
  HealthResp,
  HumanAction,
  ImportResp,
  ImportedItem,
  Involvement,
  PreviewResp,
  ProjectState,
  Repo,
  ScanResult,
  SyncStatus,
  Taxonomy,
  TrackerKind,
  TrackerStatus,
  TrackerView,
  VerifyResp,
  Weights,
  WorkItem,
} from './types.ts'

/** `{ "ok": true }` — the shared trivial-success envelope. */
export interface OkResp {
  ok: boolean
}

// ── Base URL resolution (synchronous, once at module load) ─────────────────────

export function resolveBaseUrl(): string {
  // 1. Tauri: the shell injected the daemon origin (window.__GITSTATE_API__)
  //    via an init script before the webview loaded.
  if (typeof window !== 'undefined' && window.__GITSTATE_API__) {
    return window.__GITSTATE_API__
  }
  // 2. Headless / browser served by the daemon (or vite dev with a proxy):
  //    same-origin — empty prefix, relative paths.
  return ''
}

const BASE = resolveBaseUrl()
const url = (p: string) => `${BASE}${p}`

// ── Error type ─────────────────────────────────────────────────────────────────

export class ApiError extends Error {
  status: number
  /** snake_case error code from the daemon. */
  code: string | null
  body: unknown

  constructor(status: number, message: string, code: string | null = null, body: unknown = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.body = body
  }
}

/** Shape of the daemon's JSON error envelope, narrowed defensively. */
interface ErrorEnvelope {
  error?: string
  message?: string
  code?: string
}

function isErrorEnvelope(v: unknown): v is ErrorEnvelope {
  return typeof v === 'object' && v !== null
}

// ── Core request ───────────────────────────────────────────────────────────────

async function parseResponse(res: Response): Promise<unknown> {
  if (res.status === 204) return null
  const ct = res.headers.get('Content-Type') ?? ''
  const isJson = ct.includes('application/json')

  if (!res.ok) {
    let body: unknown = null
    let message = `HTTP ${res.status}`
    let code: string | null = null
    if (isJson) {
      try {
        body = await res.json()
        // Daemon error envelope: { error, code }
        if (isErrorEnvelope(body)) {
          message = body.error || body.message || message
          code = body.code ?? null
        }
      } catch { /* ignore parse errors */ }
    }
    throw new ApiError(res.status, message, code, body)
  }

  if (isJson) return res.json()
  return res.text()
}

export interface RequestOptions {
  headers?: Record<string, string>
  signal?: AbortSignal
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  const headers = { ...(options.headers || {}) }
  if (body != null) headers['Content-Type'] = 'application/json'

  let res: Response
  try {
    res = await fetch(url(path), {
      method,
      headers,
      body: body != null ? JSON.stringify(body) : undefined,
      signal: options.signal,
    })
  } catch (err) {
    // Network / daemon-unreachable — surface a typed error the UI can render.
    throw new ApiError(0, 'Cannot reach the gitstate daemon.', 'daemon_unreachable', { cause: String(err) })
  }
  // The daemon is the sole source of truth for this shape; the generic `T` at
  // each call site is what keeps the rest of the app honestly typed.
  return parseResponse(res) as Promise<T>
}

export function get<T>(path: string, options?: RequestOptions): Promise<T> {
  return request<T>('GET', path, undefined, options)
}
export function post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  return request<T>('POST', path, body, options)
}
export function put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  return request<T>('PUT', path, body, options)
}
export function patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  return request<T>('PATCH', path, body, options)
}
export function del<T>(path: string, options?: RequestOptions): Promise<T> {
  return request<T>('DELETE', path, undefined, options)
}

/** A query-param value; `null`/`undefined`/`''` are omitted entirely. */
type QueryValue = string | number | boolean | null | undefined

// Build a query string from a plain object, skipping null/undefined/'' values.
function qs(params?: Record<string, QueryValue>): string {
  if (!params) return ''
  const usp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v == null || v === '') continue
    usp.set(k, String(v))
  }
  const s = usp.toString()
  return s ? `?${s}` : ''
}

// ── Typed calls (daemon HTTP API, spec §3 / web contract §7) ───────────────────

/** GET /health — daemon liveness + capabilities. */
export function health(): Promise<HealthResp> {
  return get<HealthResp>('/health')
}

// Repos --------------------------------------------------------------------------

/** GET /api/repos — every registered repo. */
export function listRepos(): Promise<Repo[]> {
  return get<Repo[]>('/api/repos')
}

export interface AddRepoInput {
  path?: string
  remote_url?: string
}

/** POST /api/repos — register a repo by local `path` OR by `remote_url`. */
export function addRepo(body: AddRepoInput): Promise<Repo> {
  return post<Repo>('/api/repos', body)
}

/** DELETE /api/repos/:id */
export function deleteRepo(id: string): Promise<DeletedResp> {
  return del<DeletedResp>(`/api/repos/${encodeURIComponent(id)}`)
}

export interface ScanOptions {
  with_forge?: boolean
  since?: string
}

/** POST /api/repos/:id/scan — walk git (and optionally the forge) to derive state. */
export function scanRepo(id: string, opts: ScanOptions = {}): Promise<ScanResult> {
  return post<ScanResult>(`/api/repos/${encodeURIComponent(id)}/scan`, {
    with_forge: opts.with_forge ?? true,
    ...(opts.since ? { since: opts.since } : {}),
  })
}

/** GET /api/repos/:id/project-state */
export function projectState(id: string): Promise<ProjectState> {
  return get<ProjectState>(`/api/repos/${encodeURIComponent(id)}/project-state`)
}

export interface RangeOptions {
  from?: string
  to?: string
}

/** GET /api/repos/:id/contributions?from=&to= */
export function contributions(id: string, { from, to }: RangeOptions = {}): Promise<Contribution[]> {
  return get<Contribution[]>(`/api/repos/${encodeURIComponent(id)}/contributions${qs({ from, to })}`)
}

export interface WorkItemQuery {
  kind?: string
  state?: string
}

/** GET /api/repos/:id/work-items?kind=&state= */
export function workItems(id: string, { kind, state }: WorkItemQuery = {}): Promise<WorkItem[]> {
  return get<WorkItem[]>(`/api/repos/${encodeURIComponent(id)}/work-items${qs({ kind, state })}`)
}

/** GET /api/repos/:id/classifications — stored labels; judges nothing. */
export function repoClassifications(id: string): Promise<Classification[]> {
  return get<Classification[]>(`/api/repos/${encodeURIComponent(id)}/classifications`)
}

/** GET /api/repos/:id/effort — stored difficulty estimates; judges nothing. */
export function repoEffort(id: string): Promise<EffortEstimate[]> {
  return get<EffortEstimate[]>(`/api/repos/${encodeURIComponent(id)}/effort`)
}

/** GET /api/contributors — merged identities across all repos. */
export function contributors(): Promise<Contributor[]> {
  return get<Contributor[]>('/api/contributors')
}

export interface AnalyticsOptions {
  repo_id?: string
  days?: number
  from?: string
  to?: string
}

/**
 * GET /api/analytics — the single round-trip behind the dashboard and insights
 * screens: dense heatmap, weekly/cycle/throughput series, contributor rollup
 * and headline totals.
 *
 * The window anchors on the newest commit rather than on wall-clock now, so a
 * repo last scanned months ago still renders a populated view.
 */
export function analytics({ repo_id, days, from, to }: AnalyticsOptions = {}): Promise<Analytics> {
  return get<Analytics>(`/api/analytics${qs({ repo_id, days, from, to })}`)
}

// Contribution weights --------------------------------------------------------

/** GET /api/weights — the six-dimension composite weights. */
export function weights(): Promise<Weights> {
  return get<Weights>('/api/weights')
}

/** PUT /api/weights — persist new weights; the daemon returns them normalized. */
export function saveWeights(body: Weights): Promise<Weights> {
  return put<Weights>('/api/weights', body)
}

/** POST /api/weights/reset — back to the shipped defaults. */
export function resetWeights(): Promise<Weights> {
  return post<Weights>('/api/weights/reset')
}

// Engineering health + involvement ---------------------------------------------

export interface WindowOptions {
  days?: number
  repo_id?: string
}

/** GET /api/health-metrics — DORA, bus factor, review and quality rollups. */
export function healthMetrics({ days, repo_id }: WindowOptions = {}): Promise<EngHealth> {
  return get<EngHealth>(`/api/health-metrics${qs({ days, repo_id })}`)
}

/** GET /api/involvement — who touches which repo, both directions. */
export function involvement({ days, repo_id }: WindowOptions = {}): Promise<Involvement> {
  return get<Involvement>(`/api/involvement${qs({ days, repo_id })}`)
}

/** GET /api/contributions/rollup — six dimensions merged across every repo. */
export function contributionRollup({ from, to }: RangeOptions = {}): Promise<ContributionRollupRow[]> {
  return get<ContributionRollupRow[]>(`/api/contributions/rollup${qs({ from, to })}`)
}

// Trackers (Jira / Linear, local personal tokens) --------------------------------

/** GET /api/trackers — configured trackers; tokens come back masked. */
export function trackers(): Promise<TrackerView[]> {
  return get<TrackerView[]>('/api/trackers')
}

export interface TrackerInput {
  base_url: string
  email: string
  token: string
  project: string
}

/** PUT /api/trackers/:kind — save a credential ({ base_url, email, token, project }). */
export function saveTracker(kind: TrackerKind, body: TrackerInput): Promise<TrackerView> {
  return put<TrackerView>(`/api/trackers/${encodeURIComponent(kind)}`, body)
}

/** DELETE /api/trackers/:kind — forget the stored credential. */
export function deleteTracker(kind: TrackerKind): Promise<DeletedResp> {
  return del<DeletedResp>(`/api/trackers/${encodeURIComponent(kind)}`)
}

/** POST /api/trackers/:kind/test — verify without importing. */
export function testTracker(kind: TrackerKind): Promise<TrackerStatus> {
  return post<TrackerStatus>(`/api/trackers/${encodeURIComponent(kind)}/test`)
}

export interface ImportPreviewOptions {
  kind?: string
  limit?: number
}

/** POST /api/import/preview — fetch a sample without persisting anything. */
export function importPreview({ kind, limit }: ImportPreviewOptions = {}): Promise<PreviewResp> {
  return post<PreviewResp>('/api/import/preview', { kind, ...(limit ? { limit } : {}) })
}

export interface ImportRunOptions {
  kind?: string
  repo_id?: string
  limit?: number
}

/** POST /api/import/run — fetch and persist against a repo. */
export function importRun({ kind, repo_id, limit }: ImportRunOptions = {}): Promise<ImportResp> {
  return post<ImportResp>('/api/import/run', { kind, repo_id, ...(limit ? { limit } : {}) })
}

export interface ImportFileOptions {
  source?: string
  repo_id?: string
  content?: string
}

/** POST /api/import/file — offline import from an exported CSV/JSON payload. */
export function importFile({ source, repo_id, content }: ImportFileOptions = {}): Promise<ImportResp> {
  return post<ImportResp>('/api/import/file', { source, repo_id, content })
}

// ImportedItem re-exported for callers that render preview rows.
export type { ImportedItem }

// Contexts (CRDT-backed saved working sets) --------------------------------------

export function listContexts(): Promise<Context[]> {
  return get<Context[]>('/api/contexts')
}
export function getContext(id: string): Promise<Context> {
  return get<Context>(`/api/contexts/${encodeURIComponent(id)}`)
}
export function createContext(body: ContextInput): Promise<Context> {
  return post<Context>('/api/contexts', body)
}
export function patchContext(id: string, patchBody: Partial<ContextInput>): Promise<Context> {
  return patch<Context>(`/api/contexts/${encodeURIComponent(id)}`, patchBody)
}
export function deleteContext(id: string): Promise<DeletedResp> {
  return del<DeletedResp>(`/api/contexts/${encodeURIComponent(id)}`)
}

// Categories (CRDT-backed) -------------------------------------------------------

export function listCategories(): Promise<Category[]> {
  return get<Category[]>('/api/categories')
}
export function createCategory(body: CategoryInput): Promise<Category> {
  return post<Category>('/api/categories', body)
}
export function patchCategory(id: string, body: Partial<CategoryInput>): Promise<Category> {
  return patch<Category>(`/api/categories/${encodeURIComponent(id)}`, body)
}
export function deleteCategory(id: string): Promise<DeletedResp> {
  return del<DeletedResp>(`/api/categories/${encodeURIComponent(id)}`)
}

// Classification + effort --------------------------------------------------------

export interface ClassifyOptions {
  repo_id?: string
  item_ids?: string[]
}

/** POST /api/classify — classify work items (all uncategorized when item_ids omitted). */
export function classify({ repo_id, item_ids }: ClassifyOptions = {}): Promise<Classification[]> {
  return post<Classification[]>('/api/classify', { repo_id, ...(item_ids ? { item_ids } : {}) })
}

export interface ClassifyFeedbackInput {
  item_id: string
  category_key: string
}

/** POST /api/classify/feedback — record the user's chosen category (local learning). */
export function classifyFeedback({ item_id, category_key }: ClassifyFeedbackInput): Promise<OkResp> {
  return post<OkResp>('/api/classify/feedback', { item_id, category_key })
}

/** POST /api/effort — judge diff-difficulty for work items. */
export function effort({ repo_id, item_ids }: ClassifyOptions = {}): Promise<EffortEstimate[]> {
  return post<EffortEstimate[]>('/api/effort', { repo_id, ...(item_ids ? { item_ids } : {}) })
}

// Taxonomy + sync ----------------------------------------------------------------

/** GET /api/taxonomy — the full signed taxonomy document. */
export function taxonomy(): Promise<Taxonomy> {
  return get<Taxonomy>('/api/taxonomy')
}

/** POST /api/taxonomy/verify — verify a taxonomy doc's signature. */
export function verifyTaxonomy(doc: Taxonomy): Promise<VerifyResp> {
  return post<VerifyResp>('/api/taxonomy/verify', doc)
}

/** GET /api/sync/status — P2P sync state ({ enabled:false, … } when off). */
export function syncStatus(): Promise<SyncStatus> {
  return get<SyncStatus>('/api/sync/status')
}

// ── Agent runs (the AI-agent write path) ────────────────────────────────────────

export interface NewAgentRunInput {
  goal: string
  repo_id?: string
  pr_id?: string
  issue_id?: string
  supervisor_id?: string
  agent_name?: string
  branch?: string
  diff_summary?: AgentDiffSummary
  tests_passed?: boolean
  human_action?: HumanAction
  iterations?: number
  cost_usd?: number
}

/** POST /api/agent-runs — record one agent run. Only `goal` is required. */
export function logAgentRun(body: NewAgentRunInput): Promise<AgentRun> {
  return post<AgentRun>('/api/agent-runs', body)
}

export interface AgentRunQuery {
  repo_id?: string
  pr_id?: string
  issue_id?: string
  agent_name?: string
  limit?: number
}

/** GET /api/agent-runs — logged agent runs, newest-first. */
export function listAgentRuns({
  repo_id,
  pr_id,
  issue_id,
  agent_name,
  limit,
}: AgentRunQuery = {}): Promise<AgentRun[]> {
  return get<AgentRun[]>(`/api/agent-runs${qs({ repo_id, pr_id, issue_id, agent_name, limit })}`)
}

// ── External links ──────────────────────────────────────────────────────────────

/**
 * Open a forge / PR / docs link. In the Tauri shell this routes through the
 * `open_external` command so the OS browser handles it; headless falls back to
 * a normal new-tab window.open.
 */
export function openExternal(target: string | null | undefined): void {
  if (!target) return
  const invoke =
    typeof window !== 'undefined' &&
    (window.__TAURI_INTERNALS__?.invoke || window.__TAURI__?.core?.invoke)
  if (invoke) {
    try {
      invoke('open_external', { url: target })
      return
    } catch { /* fall through to window.open */ }
  }
  if (typeof window !== 'undefined') {
    window.open(target, '_blank', 'noopener,noreferrer')
  }
}
