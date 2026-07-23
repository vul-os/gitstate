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

// ── Base URL resolution (synchronous, once at module load) ─────────────────────

export function resolveBaseUrl() {
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
const url = (p) => `${BASE}${p}`

// ── Error type ─────────────────────────────────────────────────────────────────

export class ApiError extends Error {
  /**
   * @param {number} status
   * @param {string} message
   * @param {string|null} code   snake_case error code from the daemon
   * @param {unknown} body
   */
  constructor(status, message, code = null, body = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.body = body
  }
}

// ── Core request ───────────────────────────────────────────────────────────────

async function parseResponse(res) {
  if (res.status === 204) return null
  const ct = res.headers.get('Content-Type') ?? ''
  const isJson = ct.includes('application/json')

  if (!res.ok) {
    let body = null
    let message = `HTTP ${res.status}`
    let code = null
    if (isJson) {
      try {
        body = await res.json()
        // Daemon error envelope: { error, code }
        message = body?.error || body?.message || message
        code = body?.code ?? null
      } catch { /* ignore parse errors */ }
    }
    throw new ApiError(res.status, message, code, body)
  }

  if (isJson) return res.json()
  return res.text()
}

async function request(method, path, body, options = {}) {
  const headers = { ...(options.headers || {}) }
  if (body != null) headers['Content-Type'] = 'application/json'

  let res
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
  return parseResponse(res)
}

export function get(path, options) { return request('GET', path, null, options) }
export function post(path, body, options) { return request('POST', path, body, options) }
export function patch(path, body, options) { return request('PATCH', path, body, options) }
export function del(path, options) { return request('DELETE', path, null, options) }

// Build a query string from a plain object, skipping null/undefined/'' values.
function qs(params) {
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
export function health() { return get('/health') }

// Repos --------------------------------------------------------------------------

/** GET /api/repos — every registered repo. */
export function listRepos() { return get('/api/repos') }

/**
 * POST /api/repos — register a repo by local `path` OR by `remote_url`.
 * @param {{ path?: string, remote_url?: string }} body
 */
export function addRepo(body) { return post('/api/repos', body) }

/** DELETE /api/repos/:id */
export function deleteRepo(id) { return del(`/api/repos/${encodeURIComponent(id)}`) }

/**
 * POST /api/repos/:id/scan — walk git (and optionally the forge) to derive state.
 * @param {string} id
 * @param {{ with_forge?: boolean, since?: string }} [opts]
 */
export function scanRepo(id, opts = {}) {
  return post(`/api/repos/${encodeURIComponent(id)}/scan`, {
    with_forge: opts.with_forge ?? true,
    ...(opts.since ? { since: opts.since } : {}),
  })
}

/** GET /api/repos/:id/project-state */
export function projectState(id) {
  return get(`/api/repos/${encodeURIComponent(id)}/project-state`)
}

/** GET /api/repos/:id/contributions?from=&to= */
export function contributions(id, { from, to } = {}) {
  return get(`/api/repos/${encodeURIComponent(id)}/contributions${qs({ from, to })}`)
}

/** GET /api/repos/:id/work-items?kind=&state= */
export function workItems(id, { kind, state } = {}) {
  return get(`/api/repos/${encodeURIComponent(id)}/work-items${qs({ kind, state })}`)
}

/** GET /api/contributors — merged identities across all repos. */
export function contributors() { return get('/api/contributors') }

// Contexts (CRDT-backed saved working sets) --------------------------------------

export function listContexts() { return get('/api/contexts') }
export function getContext(id) { return get(`/api/contexts/${encodeURIComponent(id)}`) }
export function createContext(body) { return post('/api/contexts', body) }
export function patchContext(id, patchBody) { return patch(`/api/contexts/${encodeURIComponent(id)}`, patchBody) }
export function deleteContext(id) { return del(`/api/contexts/${encodeURIComponent(id)}`) }

// Categories (CRDT-backed) -------------------------------------------------------

export function listCategories() { return get('/api/categories') }
export function createCategory(body) { return post('/api/categories', body) }
export function patchCategory(id, body) { return patch(`/api/categories/${encodeURIComponent(id)}`, body) }
export function deleteCategory(id) { return del(`/api/categories/${encodeURIComponent(id)}`) }

// Classification + effort --------------------------------------------------------

/** POST /api/classify — classify work items (all uncategorized when item_ids omitted). */
export function classify({ repo_id, item_ids } = {}) {
  return post('/api/classify', { repo_id, ...(item_ids ? { item_ids } : {}) })
}

/** POST /api/classify/feedback — record the user's chosen category (local learning). */
export function classifyFeedback({ item_id, category_key }) {
  return post('/api/classify/feedback', { item_id, category_key })
}

/** POST /api/effort — judge diff-difficulty for work items. */
export function effort({ repo_id, item_ids } = {}) {
  return post('/api/effort', { repo_id, ...(item_ids ? { item_ids } : {}) })
}

// Taxonomy + sync ----------------------------------------------------------------

/** GET /api/taxonomy — the full signed taxonomy document. */
export function taxonomy() { return get('/api/taxonomy') }

/** POST /api/taxonomy/verify — verify a taxonomy doc's signature. */
export function verifyTaxonomy(doc) { return post('/api/taxonomy/verify', doc) }

/** GET /api/sync/status — P2P sync state ({ enabled:false, … } when off). */
export function syncStatus() { return get('/api/sync/status') }

// ── External links ──────────────────────────────────────────────────────────────

/**
 * Open a forge / PR / docs link. In the Tauri shell this routes through the
 * `open_external` command so the OS browser handles it; headless falls back to
 * a normal new-tab window.open.
 */
export function openExternal(target) {
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
