/**
 * gitstate e2e test runner — a tiny Playwright harness built on the `playwright`
 * library only (NOT @playwright/test, which is not installed).
 *
 * What it does:
 *   - Launches one chromium instance and reuses it across all specs.
 *   - Exposes a `test(name, fn)` registry (specs call it at import time).
 *   - Gives each spec a fresh BrowserContext + Page so specs are isolated.
 *   - Captures `pageerror` (uncaught exceptions) and `console.error` per spec
 *     and FAILS the spec if any occur (allowlisted noise is filtered).
 *   - Provides a shared `login(page)` helper that drives the real login form.
 *   - Runs the core suite in BOTH dark and light themes (configurable).
 *   - Prints a green/red summary and exits non-zero if anything failed.
 *
 * Usage:
 *   node tests/runner.mjs                 # both themes (dark + light)
 *   node tests/runner.mjs --theme=light   # light only
 *   node tests/runner.mjs --theme=dark    # dark only
 *   node tests/runner.mjs --headed        # show the browser
 *   node tests/runner.mjs --grep=board    # only specs whose name matches
 *
 * Env:
 *   BASE_URL    web app base (default http://localhost:5173)
 *   API_URL     Go API base (default http://localhost:8080)
 *   EMAIL       login email (default demo@gitstate.dev)
 *   PASSWORD    login password (default demo1234)
 *   HEADLESS    "0"/"false" to show the browser
 */
import { chromium } from 'playwright'
import { readdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, resolve, join } from 'node:path'

const __dirname = dirname(fileURLToPath(import.meta.url))

// ── Config ──────────────────────────────────────────────────────────────────
const argv = process.argv.slice(2)
const argTheme = (argv.find((a) => a.startsWith('--theme=')) || '').split('=')[1]
const grep = (argv.find((a) => a.startsWith('--grep=')) || '').split('=')[1] || ''
const headed = argv.includes('--headed')

export const CONFIG = {
  baseUrl: (process.env.BASE_URL || 'http://localhost:5173').replace(/\/$/, ''),
  apiUrl: (process.env.API_URL || 'http://localhost:8080').replace(/\/$/, ''),
  email: process.env.EMAIL || 'demo@gitstate.dev',
  password: process.env.PASSWORD || 'demo1234',
  headless: headed ? false : !/^(0|false)$/i.test(process.env.HEADLESS || ''),
  navTimeout: 60_000,
  themes: argTheme === 'light' ? ['light'] : argTheme === 'dark' ? ['dark'] : ['dark', 'light'],
}

const THEME_KEY = 'gs-theme' // web/src/lib/theme.jsx

// ── ANSI helpers ────────────────────────────────────────────────────────────
const c = {
  green: (s) => `\x1b[32m${s}\x1b[0m`,
  red: (s) => `\x1b[31m${s}\x1b[0m`,
  yellow: (s) => `\x1b[33m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  bold: (s) => `\x1b[1m${s}\x1b[0m`,
  cyan: (s) => `\x1b[36m${s}\x1b[0m`,
}

// ── Test registry ───────────────────────────────────────────────────────────
/** @type {{name:string, fn:Function, opts:object}[]} */
const registry = []

/**
 * Register a spec.
 * @param {string} name
 * @param {(ctx:{page:import('playwright').Page, context:import('playwright').BrowserContext, theme:string}) => Promise<void>} fn
 * @param {{authed?:boolean, themes?:boolean}} [opts]  authed: login first; themes: run in every theme (default true)
 */
export function test(name, fn, opts = {}) {
  // seedAuth (default true): inject the shared login snapshot into the context's
  // localStorage before app JS runs, so the spec is already authed and never
  // POSTs /auth/login from the browser. Specs that exercise the login form
  // itself (or expect the anonymous /login page) opt out with seedAuth:false.
  registry.push({ name, fn, opts: { authed: false, themes: true, seedAuth: true, ...opts } })
}

// ── Console/pageerror tracking ──────────────────────────────────────────────
// Some console errors are unavoidable third-party / dev noise. Keep this list
// tight so real app errors still fail the spec.
const IGNORED_CONSOLE = [
  /favicon/i,
  /Download the React DevTools/i,
  /\[vite\]/i,
  /ResizeObserver loop/i,
  /Failed to load resource: the server responded with a status of 404/i, // optional assets
  /the server responded with a status of 401/i, // anonymous config probes before/after auth
  /^Event$/, // admin console SSE/EventSource connection 'error' events log as bare "Event"
  /EventSource|event ?source/i, // SSE reconnect noise on the admin console
]

function attachErrorCapture(page, sink) {
  page.on('pageerror', (err) => {
    sink.push({ kind: 'pageerror', text: String(err?.stack || err?.message || err) })
  })
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return
    const text = msg.text()
    if (IGNORED_CONSOLE.some((re) => re.test(text))) return
    sink.push({ kind: 'console.error', text })
  })
}

// ── Shared helpers exposed to specs ─────────────────────────────────────────

/** Resolve a full app URL from a path. */
export function url(path = '/') {
  return `${CONFIG.baseUrl}${path.startsWith('/') ? path : '/' + path}`
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** Wait for network idle without throwing if a long-poll keeps the socket open.
 *  Short timeout so an occasional slow/held connection can't accumulate toward the
 *  per-spec budget across multiple settle() calls (was causing flaky 60s timeouts). */
export async function settle(page, { extra = 150 } = {}) {
  try {
    await page.waitForLoadState('networkidle', { timeout: 4_000 })
  } catch {
    /* ignore — some pages hold a connection (chat/SSE) */
  }
  if (extra) await sleep(extra)
}

/**
 * Drive the real login form at /login and wait until the app navigates away.
 * Idempotent: returns early if already authed (token present in localStorage).
 */
export async function login(page) {
  await page.goto(url('/login'), { waitUntil: 'domcontentloaded', timeout: CONFIG.navTimeout })
  // Already authed? Login redirects to / immediately.
  const tok = await page.evaluate(() => localStorage.getItem('gs_access_token')).catch(() => null)
  if (tok) return
  await page.waitForSelector('#email', { timeout: 15_000 })
  await page.fill('#email', CONFIG.email)
  await page.fill('#password', CONFIG.password)
  await page.click('button[type="submit"]')
  // The authoritative success signal is the access token landing in
  // localStorage — more reliable than waitForURL, which depends on a SPA route
  // change firing as a navigation event and intermittently hung for the full
  // 60s navTimeout in headless. Poll the token (and the route, as a backup) on a
  // tight budget so a missed signal fails fast with a clear message rather than
  // burning the per-spec budget.
  const deadline = Date.now() + 20_000
  let authed = false
  for (;;) {
    authed = await page
      .evaluate(() => !!localStorage.getItem('gs_access_token'))
      .catch(() => false)
    if (authed) break
    // Backup: app may have routed off /login before the token read settled.
    const path = await page.evaluate(() => location.pathname).catch(() => '/login')
    if (!/\/login$/.test(path)) {
      authed = true
      break
    }
    if (Date.now() >= deadline) break
    await sleep(150)
  }
  if (!authed) throw new Error('login: access token did not appear within 20s of submit')
  await settle(page)
}

/**
 * Navigate to an authed app route, logging in if needed, and wait for content.
 * Returns when an <h1>/<main> is visible.
 */
export async function gotoApp(page, path, { waitFor = 'h1, main' } = {}) {
  await login(page)
  await page.goto(url(path), { waitUntil: 'domcontentloaded', timeout: CONFIG.navTimeout })
  if (waitFor) {
    await page.waitForSelector(waitFor, { state: 'visible', timeout: 20_000 }).catch(() => {})
  }
  await settle(page)
}

/**
 * The page-content <h1> inside <main> (authed app layout has a separate TopBar
 * <h1> with the route title; the real page heading lives in <main>).
 * Falls back to the first <h1> if there is no <main>.
 */
export async function pageHeading(page) {
  const inMain = page.locator('main h1')
  if (await inMain.count()) return (await inMain.first().innerText()).trim()
  return (await page.locator('h1').first().innerText()).trim()
}

/** Navigate to a public route and wait for content. */
export async function gotoPublic(page, path, { waitFor = 'h1, h2, main' } = {}) {
  await page.goto(url(path), { waitUntil: 'domcontentloaded', timeout: CONFIG.navTimeout })
  if (waitFor) {
    await page.waitForSelector(waitFor, { state: 'visible', timeout: 20_000 }).catch(() => {})
  }
  await settle(page)
}

// ── API helper (for cross-checking persistence without UI flakiness) ─────────
// One login for the whole suite. The /auth/login endpoint is rate-limited to
// 10 req/min per IP (internal/middleware/ratelimit.go AuthRateLimit), so the
// suite MUST NOT log in once per spec — instead we log in exactly once here and
// reuse the snapshot, both for direct API calls and for seeding the browser's
// localStorage (see authStorageInit / seedAuth). A POST to /auth/login can still
// transiently 429 if earlier runs warmed the bucket, so we retry with backoff.
let _apiToken = null
let _apiRefresh = null
let _apiOrg = null
let _apiUser = null

async function fetchWithRetry(input, init, { tries = 6, label = 'request' } = {}) {
  let lastStatus = 0
  for (let i = 0; i < tries; i++) {
    const res = await fetch(input, init)
    if (res.status !== 429) return res
    lastStatus = 429
    // Token bucket refills at perMin/60 ~= 1 token / 6s for the auth limiter.
    // Back off generously so we don't hammer the bucket while it's empty.
    await sleep(Math.min(8_000, 1_500 + i * 1_500))
  }
  throw new Error(`${label}: rate-limited (HTTP ${lastStatus}) after ${tries} attempts`)
}

async function apiLogin() {
  if (_apiToken && _apiOrg) return
  const res = await fetchWithRetry(
    `${CONFIG.apiUrl}/auth/login`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: CONFIG.email, password: CONFIG.password }),
    },
    { label: 'api login' },
  )
  if (!res.ok) throw new Error(`api login failed: ${res.status}`)
  const data = await res.json()
  _apiToken = data.accessToken
  _apiRefresh = data.refreshToken || null
  _apiUser = data.user || null
  // Resolve an org id from /api/orgs
  const orgsRes = await fetch(`${CONFIG.apiUrl}/api/orgs`, {
    headers: { Authorization: `Bearer ${_apiToken}` },
  })
  if (orgsRes.ok) {
    const orgs = await orgsRes.json()
    const list = Array.isArray(orgs) ? orgs : orgs.orgs || []
    _apiOrg = list[0]?.id || null
  }
}

/**
 * Build the localStorage entries that make the SPA consider itself logged in,
 * using the single shared API login. Seeding these into a context lets authed
 * specs skip the browser login form entirely — which is what keeps the suite
 * under the 10/min /auth/login rate limit. Keys mirror web/src/lib/api.js.
 */
export async function authStorageInit() {
  await apiLogin()
  /** @type {Record<string,string>} */
  const entries = {}
  if (_apiToken) entries['gs_access_token'] = _apiToken
  if (_apiRefresh) entries['gs_refresh_token'] = _apiRefresh
  if (_apiOrg) entries['gs_active_org'] = _apiOrg
  return entries
}

/** GET an /api/* path with auth + org header. Returns parsed JSON. */
export async function api(path) {
  await apiLogin()
  const res = await fetch(`${CONFIG.apiUrl}${path}`, {
    headers: {
      Authorization: `Bearer ${_apiToken}`,
      ...(_apiOrg ? { 'X-Org-ID': _apiOrg } : {}),
    },
  })
  if (!res.ok) throw new Error(`api GET ${path} -> ${res.status}`)
  return res.json()
}

/** PATCH an /api/* path with auth + org header. Used to restore state after tests. */
export async function apiPatch(path, body) {
  await apiLogin()
  const res = await fetch(`${CONFIG.apiUrl}${path}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${_apiToken}`,
      ...(_apiOrg ? { 'X-Org-ID': _apiOrg } : {}),
    },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`api PATCH ${path} -> ${res.status}`)
  return res.json()
}

// ── Assertions ──────────────────────────────────────────────────────────────
/**
 * Assert helper. On failure throws an Error whose message names the page +
 * the failed expectation (specs pass a descriptive label).
 */
export function assert(cond, message) {
  if (!cond) throw new Error(message || 'assertion failed')
}

/** Assert an element/locator is visible; message names what was expected. */
export async function assertVisible(locator, label) {
  const count = await locator.count()
  if (count === 0) throw new Error(`${label}: expected element to exist but found none`)
  const visible = await locator.first().isVisible()
  if (!visible) throw new Error(`${label}: element exists but is not visible`)
}

// ── Spec loading ────────────────────────────────────────────────────────────
async function loadSpecs() {
  const dir = join(__dirname, 'e2e')
  const files = (await readdir(dir)).filter((f) => f.endsWith('.mjs')).sort()
  for (const f of files) {
    await import(resolve(dir, f))
  }
}

// ── Runner ──────────────────────────────────────────────────────────────────
async function runSpec(browser, spec, theme) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    colorScheme: theme === 'light' ? 'light' : 'dark',
    reducedMotion: 'reduce',
  })
  context.setDefaultNavigationTimeout(CONFIG.navTimeout)
  context.setDefaultTimeout(20_000)
  // Seed localStorage before any app JS runs: theme always, plus the shared auth
  // snapshot for authed specs so they skip the rate-limited browser login form.
  let seed = { [THEME_KEY]: theme }
  if (spec.opts.seedAuth) {
    try {
      seed = { ...seed, ...(await authStorageInit()) }
    } catch (e) {
      // Surface auth-seed failures as the spec error rather than a silent
      // unauthenticated run that fails confusingly downstream.
      await context.close().catch(() => {})
      return { error: e, ms: 0 }
    }
  }
  await context.addInitScript((kv) => {
    try {
      for (const [k, v] of Object.entries(kv)) window.localStorage.setItem(k, v)
    } catch {
      /* ignore */
    }
  }, seed)

  const page = await context.newPage()
  const errors = []
  attachErrorCapture(page, errors)

  const started = Date.now()
  let error = null
  try {
    await spec.fn({ page, context, theme })
    // Fail on any captured uncaught/console error.
    if (errors.length) {
      const lines = errors.map((e) => `    [${e.kind}] ${e.text.split('\n')[0]}`).join('\n')
      throw new Error(`page reported ${errors.length} error(s):\n${lines}`)
    }
  } catch (e) {
    error = e
  } finally {
    await context.close().catch(() => {})
  }
  return { error, ms: Date.now() - started }
}

async function main() {
  await loadSpecs()

  let specs = registry
  if (grep) specs = specs.filter((s) => s.name.toLowerCase().includes(grep.toLowerCase()))
  if (specs.length === 0) {
    console.error(c.red(`No specs matched (grep="${grep}").`))
    process.exit(1)
  }

  console.log(c.bold('\ngitstate e2e suite'))
  console.log(c.dim(`  base:    ${CONFIG.baseUrl}`))
  console.log(c.dim(`  api:     ${CONFIG.apiUrl}`))
  console.log(c.dim(`  themes:  ${CONFIG.themes.join(', ')}`))
  console.log(c.dim(`  headless:${CONFIG.headless}`))
  console.log('')

  const browser = await chromium.launch({ headless: CONFIG.headless })
  const results = []

  for (const theme of CONFIG.themes) {
    // Theme-tagged run header (only print if more than one theme).
    if (CONFIG.themes.length > 1) console.log(c.cyan(c.bold(`── theme: ${theme} ──`)))
    for (const spec of specs) {
      // Specs marked themes:false run only in the first theme to save time.
      if (!spec.opts.themes && theme !== CONFIG.themes[0]) continue
      process.stdout.write(`  ${spec.name} ${c.dim('…')}`)
      const { error, ms } = await runSpec(browser, spec, theme)
      results.push({ name: spec.name, theme, error, ms })
      // Clear the line and reprint with status.
      process.stdout.write('\r\x1b[2K')
      if (error) {
        console.log(`  ${c.red('✗')} ${spec.name} ${c.dim(`(${ms}ms)`)}`)
        console.log(c.red(`      ${String(error.message || error).replace(/\n/g, '\n      ')}`))
      } else {
        console.log(`  ${c.green('✓')} ${spec.name} ${c.dim(`(${ms}ms)`)}`)
      }
    }
    if (CONFIG.themes.length > 1) console.log('')
  }

  await browser.close()

  const failed = results.filter((r) => r.error)
  const passed = results.length - failed.length
  console.log(c.bold('──────── summary ────────'))
  console.log(`  ${c.green(`${passed} passed`)}  ${failed.length ? c.red(`${failed.length} failed`) : c.dim('0 failed')}  ${c.dim(`(${results.length} total)`)}`)
  if (failed.length) {
    console.log('')
    for (const f of failed) {
      console.log(c.red(`  ✗ ${f.name} [${f.theme}]`))
      console.log(c.dim(`      ${String(f.error.message || f.error).split('\n')[0]}`))
    }
    process.exit(1)
  }
  console.log(c.green('\n  all green ✓\n'))
  process.exit(0)
}

main().catch((err) => {
  console.error(c.red('\nFATAL runner error:'), err)
  process.exit(1)
})
