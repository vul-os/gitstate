/**
 * gitstate API client
 * Thin fetch wrappers + token storage. Refresh-token rotation is Wave B.
 */

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

// ── Token storage ─────────────────────────────────────────────────────────────

const TOKEN_KEY = 'gs_access_token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token) {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
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

// ── Core fetch ────────────────────────────────────────────────────────────────

async function request(method, path, body, options = {}) {
  const headers = {
    'Content-Type': 'application/json',
    ...options.headers,
  }

  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body != null ? JSON.stringify(body) : undefined,
    signal: options.signal,
  })

  // Store updated token if server rotates it
  const newToken = res.headers.get('X-Access-Token')
  if (newToken) setToken(newToken)

  if (!res.ok) {
    let errBody = null
    try {
      errBody = await res.json()
    } catch {
      // ignore parse error
    }
    const msg =
      (errBody && (errBody.error || errBody.message)) ||
      `HTTP ${res.status}`
    throw new ApiError(res.status, msg, errBody)
  }

  // 204 No Content
  if (res.status === 204) return null

  const ct = res.headers.get('Content-Type') ?? ''
  if (ct.includes('application/json')) {
    return res.json()
  }
  return res.text()
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

export function del(path, options) {
  return request('DELETE', path, null, options)
}

// ── Auth helpers ──────────────────────────────────────────────────────────────

/**
 * @returns {Promise<{ token: string, user: object }>}
 */
export async function login(email, password) {
  const data = await post('/auth/login', { email, password })
  if (data?.token) setToken(data.token)
  return data
}

/**
 * @returns {Promise<{ token: string, user: object }>}
 */
export async function signup(email, password, name) {
  const data = await post('/auth/signup', { email, password, name })
  if (data?.token) setToken(data.token)
  return data
}

export function logout() {
  clearToken()
}

/**
 * Fetch public config — used by login page to discover enabled OAuth providers.
 * Shape: { publicUrl, auth: { password, providers: { google, microsoft } }, billing: { chargeCurrency } }
 * @returns {Promise<object>}
 */
export function fetchConfig() {
  return get('/api/config')
}
