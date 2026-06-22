/**
 * gitstate API client
 * Token storage, refresh-token rotation with 401 retry, auth helpers.
 * Org scoping: X-Org-ID header is injected on all /api/* requests (not /auth/*).
 */

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

// ── Org storage ───────────────────────────────────────────────────────────────

const ACTIVE_ORG_KEY = 'gs_active_org'

/** Returns the stored active org id, or null. */
export function getActiveOrgId() {
  return localStorage.getItem(ACTIVE_ORG_KEY) ?? null
}

// ── Token storage ─────────────────────────────────────────────────────────────

const ACCESS_KEY = 'gs_access_token'
const REFRESH_KEY = 'gs_refresh_token'

export function getToken() {
  return localStorage.getItem(ACCESS_KEY)
}

export function getRefreshToken() {
  return localStorage.getItem(REFRESH_KEY)
}

export function setToken(token) {
  if (token) {
    localStorage.setItem(ACCESS_KEY, token)
  } else {
    localStorage.removeItem(ACCESS_KEY)
  }
}

export function setRefreshToken(token) {
  if (token) {
    localStorage.setItem(REFRESH_KEY, token)
  } else {
    localStorage.removeItem(REFRESH_KEY)
  }
}

/** Persist both tokens at once (login / signup / refresh). */
export function setTokenPair(accessToken, refreshToken) {
  setToken(accessToken)
  setRefreshToken(refreshToken)
}

export function clearTokens() {
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
}

// ── Error type ────────────────────────────────────────────────────────────────

export class ApiError extends Error {
  /**
   * @param {number} status
   * @param {string} message
   * @param {unknown} body
   */
  constructor(status, message, body = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

// ── Refresh state ─────────────────────────────────────────────────────────────

// Singleton promise to avoid multiple concurrent refresh calls
let refreshingPromise = null

/** Called when refresh fails — clears tokens and triggers redirect. */
function onAuthFailure() {
  clearTokens()
  // Soft navigation so the app re-renders and AppShell redirects to /login
  window.location.replace('/login')
}

/** Attempt to refresh the token pair. Returns new accessToken or throws. */
async function doRefresh() {
  const refreshToken = getRefreshToken()
  if (!refreshToken) throw new ApiError(401, 'No refresh token')

  const res = await fetch(`${BASE}/auth/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refreshToken }),
  })

  if (!res.ok) {
    let errBody = null
    try { errBody = await res.json() } catch { /* ignore */ }
    throw new ApiError(res.status, 'Refresh failed', errBody)
  }

  const data = await res.json()
  // Persist rotated pair
  setTokenPair(data.accessToken, data.refreshToken)
  return data.accessToken
}

// ── Core fetch ────────────────────────────────────────────────────────────────

/**
 * Internal raw request — no auto-retry.
 * @param {string} method
 * @param {string} path
 * @param {unknown} body
 * @param {object} options
 * @param {string|null} overrideToken  - Use this token instead of stored one (after refresh).
 */
async function rawRequest(method, path, body, options = {}, overrideToken = null) {
  const headers = {
    'Content-Type': 'application/json',
    ...options.headers,
  }

  const token = overrideToken ?? getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  // Inject active org on all /api/* paths (not /auth/*)
  if (path.startsWith('/api/')) {
    const orgId = getActiveOrgId()
    if (orgId) {
      headers['X-Org-ID'] = orgId
    }
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body != null ? JSON.stringify(body) : undefined,
    signal: options.signal,
  })

  return res
}

/**
 * Parse a response, throwing ApiError on non-2xx.
 */
async function parseResponse(res) {
  if (!res.ok) {
    let errBody = null
    try { errBody = await res.json() } catch { /* ignore */ }
    const msg =
      (errBody && (errBody.error || errBody.message)) ||
      `HTTP ${res.status}`
    throw new ApiError(res.status, msg, errBody)
  }

  if (res.status === 204) return null

  const ct = res.headers.get('Content-Type') ?? ''
  if (ct.includes('application/json')) {
    return res.json()
  }
  return res.text()
}

/**
 * Main request function with 401 → refresh → retry logic.
 */
async function request(method, path, body, options = {}) {
  // First attempt
  let res = await rawRequest(method, path, body, options)

  if (res.status === 401) {
    // Try to refresh exactly once
    try {
      if (!refreshingPromise) {
        refreshingPromise = doRefresh().finally(() => {
          refreshingPromise = null
        })
      }
      const newAccessToken = await refreshingPromise

      // Retry the original request with the new token
      res = await rawRequest(method, path, body, options, newAccessToken)
    } catch {
      onAuthFailure()
      throw new ApiError(401, 'Session expired. Please sign in again.')
    }
  }

  return parseResponse(res)
}

// ── Public helpers ────────────────────────────────────────────────────────────

export function get(path, options) {
  return request('GET', path, null, options)
}

export function post(path, body, options) {
  return request('POST', path, body, options)
}

export function put(path, body, options) {
  return request('PUT', path, body, options)
}

export function patch(path, body, options) {
  return request('PATCH', path, body, options)
}

export function del(path, options) {
  return request('DELETE', path, null, options)
}

// ── Auth helpers ──────────────────────────────────────────────────────────────

/**
 * Sign up a new account. Stores both tokens.
 * @returns {Promise<{ accessToken: string, refreshToken: string, user: object }>}
 */
export async function signup(email, name, password) {
  const res = await fetch(`${BASE}/auth/signup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, name, password }),
  })
  const data = await parseResponse(res)
  if (data?.accessToken) setTokenPair(data.accessToken, data.refreshToken)
  return data
}

/**
 * Sign in with email + password. Stores both tokens.
 * @returns {Promise<{ accessToken: string, refreshToken: string, user: object }>}
 */
export async function login(email, password) {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
  const data = await parseResponse(res)
  if (data?.accessToken) setTokenPair(data.accessToken, data.refreshToken)
  return data
}

/**
 * Sign out. Calls /auth/logout with the refresh token, then clears local tokens.
 * Swallows network errors (tokens are cleared regardless).
 */
export async function logout() {
  const refreshToken = getRefreshToken()
  if (refreshToken) {
    try {
      await fetch(`${BASE}/auth/logout`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refreshToken }),
      })
    } catch {
      // Network down — still clear local tokens
    }
  }
  clearTokens()
}

// ── Platform connection (OAuth-app) helpers ────────────────────────────────────

/**
 * Build the top-level navigation URL that starts the GitHub/GitLab OAuth-app
 * connect flow. The /start endpoint self-authenticates from these query params
 * because a browser navigation can't send Bearer/X-Org-ID headers.
 * @param {string} platform 'github' | 'gitlab'
 * @returns {string|null} the URL, or null if not authenticated / no active org
 */
export function connectStartUrl(platform) {
  const token = getToken()
  const orgId = getActiveOrgId()
  if (!token || !orgId) return null
  const qs = new URLSearchParams({ token, org: orgId })
  return `${BASE}/api/connect/${platform}/start?${qs.toString()}`
}

/** Fetch the org's connection status: [{platform, connected, login, configured}]. */
export function fetchConnectStatus() {
  return get('/api/connect/status')
}

/** List repos available to the stored connection token for a platform. */
export function fetchConnectRepos(platform) {
  return get(`/api/connect/${platform}/repos`)
}

/** Disconnect a platform (deletes the stored encrypted token). */
export function disconnectPlatform(platform) {
  return del(`/api/connect/${platform}`)
}

// ── Calendar connection (Google / Microsoft) helpers ───────────────────────────

/**
 * Build the top-level navigation URL that starts the Google/Microsoft calendar
 * connect flow. The /start endpoint self-authenticates from these query params
 * because a browser navigation can't send Bearer/X-Org-ID headers.
 * @param {string} provider 'google' | 'microsoft'
 * @returns {string|null} the URL, or null if not authenticated / no active org
 */
export function calendarStartUrl(provider) {
  const token = getToken()
  const orgId = getActiveOrgId()
  if (!token || !orgId) return null
  const qs = new URLSearchParams({ token, org: orgId })
  return `${BASE}/api/calendar/${provider}/start?${qs.toString()}`
}

/** Fetch the member's calendar status: [{provider, connected, configured, email, pushLeave, pullBusy}]. */
export function fetchCalendarStatus() {
  return get('/api/calendar/status')
}

/** Toggle pushLeave/pullBusy on a calendar connection. */
export function patchCalendar(provider, body) {
  return patch(`/api/calendar/${provider}`, body)
}

/** Disconnect a calendar provider (deletes the stored encrypted tokens). */
export function disconnectCalendar(provider) {
  return del(`/api/calendar/${provider}`)
}

// ── Accounting connection (Xero / QuickBooks) helpers ───────────────────────────

/**
 * Build the top-level navigation URL that starts the Xero/QuickBooks OAuth
 * connect flow. The /start endpoint self-authenticates from these query params
 * because a browser navigation can't send Bearer/X-Org-ID headers.
 * @param {string} provider 'xero' | 'quickbooks'
 * @returns {string|null} the URL, or null if not authenticated / no active org
 */
export function accountingStartUrl(provider) {
  const token = getToken()
  const orgId = getActiveOrgId()
  if (!token || !orgId) return null
  const qs = new URLSearchParams({ token, org: orgId })
  return `${BASE}/api/accounting/${provider}/start?${qs.toString()}`
}

/** Fetch the org's accounting status: [{provider, configured, connected, externalName}]. */
export function fetchAccountingStatus() {
  return get('/api/accounting/status')
}

/** Disconnect an accounting provider (deletes the stored encrypted tokens). */
export function disconnectAccounting(provider) {
  return del(`/api/accounting/${provider}`)
}

/** Push an invoice to a connected accounting provider; returns {provider, externalId, externalUrl}. */
export function pushInvoice(invoiceId, provider) {
  return post(`/api/invoices/${invoiceId}/push`, { provider })
}

/** Pull busy windows from connected calendars into availability for a period. */
export function syncCalendar(body) {
  return post('/api/calendar/sync', body ?? {})
}

/**
 * Fetch public config — used by login page to discover enabled OAuth providers.
 * Shape: { publicUrl, auth: { password, providers: { google, microsoft } }, billing: { chargeCurrency } }
 * @returns {Promise<object>}
 */
export function fetchConfig() {
  return get('/api/config')
}
