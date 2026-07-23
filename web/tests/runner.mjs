/**
 * gitstate e2e test runner — a tiny Playwright harness built on the `playwright`
 * library only (NOT @playwright/test, which is not installed).
 *
 * gitstate is a single-user local-first app: the daemon serves both the SPA and
 * the JSON API on ONE origin, and there is no auth, no orgs, no tokens. That
 * makes this harness small — launch a browser, open a route, assert.
 *
 * What it does:
 *   - Launches one chromium instance and reuses it across all specs.
 *   - Exposes a `test(name, fn)` registry (specs call it at import time).
 *   - Gives each spec a fresh BrowserContext + Page so specs are isolated.
 *   - Captures `pageerror` (uncaught exceptions) and `console.error` per spec
 *     and FAILS the spec if any occur (allowlisted noise is filtered).
 *   - Runs the suite in BOTH dark and light themes (configurable).
 *   - Prints a green/red summary and exits non-zero if anything failed.
 *
 * Prerequisites — a seeded daemon serving the built SPA:
 *   cargo run -p gitstate-cli -- seed --demo
 *   cd web && npm run build
 *   cargo run -p gitstate-cli -- serve --port 8080
 *
 * Usage:
 *   node tests/runner.mjs                 # both themes (dark + light)
 *   node tests/runner.mjs --theme=light   # light only
 *   node tests/runner.mjs --headed        # show the browser
 *   node tests/runner.mjs --grep=heatmap  # only specs whose name matches
 *
 * Env:
 *   BASE_URL    the daemon origin (default http://localhost:8080)
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
  // The daemon serves the SPA and the API on one origin — one base URL is all
  // the suite needs.
  baseUrl: (process.env.BASE_URL || 'http://localhost:8080').replace(/\/$/, ''),
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
 * @param {{themes?:boolean}} [opts]  themes: run in every theme (default true)
 */
export function test(name, fn, opts = {}) {
  registry.push({ name, fn, opts: { themes: true, ...opts } })
}

// ── Console/pageerror tracking ──────────────────────────────────────────────
// Keep this list tight so real app errors still fail the spec.
const IGNORED_CONSOLE = [
  /favicon/i,
  /Download the React DevTools/i,
  /\[vite\]/i,
  /ResizeObserver loop/i,
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

/** Wait for network idle without throwing if a connection is held open. */
export async function settle(page, { extra = 150 } = {}) {
  try {
    await page.waitForLoadState('networkidle', { timeout: 5_000 })
  } catch {
    /* ignore */
  }
  if (extra) await sleep(extra)
}

/**
 * Navigate to an app route and wait for the page heading.
 *
 * `waitFor` should name something that only exists once the daemon's data has
 * landed — otherwise a spec can assert against a still-spinning page and pass
 * or fail for the wrong reason.
 */
export async function goto(page, path, { waitFor = 'main h1' } = {}) {
  await page.goto(url(path), { waitUntil: 'domcontentloaded', timeout: CONFIG.navTimeout })
  if (waitFor) {
    await page.waitForSelector(waitFor, { state: 'visible', timeout: 20_000 }).catch(() => {})
  }
  await settle(page)
}

/** The page-content <h1> inside <main> (the TopBar carries its own route title). */
export async function pageHeading(page) {
  const inMain = page.locator('main h1')
  if (await inMain.count()) return (await inMain.first().innerText()).trim()
  return (await page.locator('h1').first().innerText()).trim()
}

/** GET a JSON path from the daemon. No auth — it's a local single-user API. */
export async function api(path) {
  const res = await fetch(url(path))
  if (!res.ok) throw new Error(`api GET ${path} -> ${res.status}`)
  return res.json()
}

// ── Assertions ──────────────────────────────────────────────────────────────

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

/** Assert a locator resolves to at least `min` elements. */
export async function assertCountAtLeast(locator, min, label) {
  const count = await locator.count()
  if (count < min) throw new Error(`${label}: expected >= ${min}, got ${count}`)
  return count
}

// ── Spec loading ────────────────────────────────────────────────────────────
async function loadSpecs() {
  const dir = join(__dirname, 'e2e')
  const files = (await readdir(dir)).filter((f) => f.endsWith('.mjs')).sort()
  for (const f of files) {
    await import(resolve(dir, f))
  }
}

// ── Preflight ───────────────────────────────────────────────────────────────
/**
 * Fail loudly and early if the daemon isn't up or the database is empty — a
 * suite that silently asserts against an empty app is worse than no suite.
 */
async function preflight() {
  let health
  try {
    health = await api('/health')
  } catch (err) {
    throw new Error(
      `cannot reach the gitstate daemon at ${CONFIG.baseUrl} (${err.message}).\n` +
        `  Start it with:  cargo run -p gitstate-cli -- serve --port 8080`,
    )
  }
  if (health?.status !== 'ok') throw new Error(`daemon /health returned ${JSON.stringify(health)}`)

  const repos = await api('/api/repos')
  if (!Array.isArray(repos) || repos.length === 0) {
    throw new Error(
      'the daemon has no repos — the suite asserts against real data.\n' +
        '  Seed it with:  cargo run -p gitstate-cli -- seed --demo',
    )
  }
  return { health, repos }
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
  // Pin the theme before any app JS runs.
  await context.addInitScript(
    ([key, value]) => {
      try {
        window.localStorage.setItem(key, value)
      } catch {
        /* ignore */
      }
    },
    [THEME_KEY, theme],
  )

  const page = await context.newPage()
  const errors = []
  attachErrorCapture(page, errors)

  const started = Date.now()
  let error = null
  try {
    await spec.fn({ page, context, theme })
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
  console.log(c.dim(`  base:     ${CONFIG.baseUrl}`))
  console.log(c.dim(`  themes:   ${CONFIG.themes.join(', ')}`))
  console.log(c.dim(`  headless: ${CONFIG.headless}`))

  try {
    const { repos } = await preflight()
    console.log(c.dim(`  repos:    ${repos.length}`))
  } catch (err) {
    console.error(`\n${c.red('preflight failed')}: ${err.message}\n`)
    process.exit(1)
  }
  console.log('')

  const browser = await chromium.launch({ headless: CONFIG.headless })
  const results = []

  for (const theme of CONFIG.themes) {
    if (CONFIG.themes.length > 1) console.log(c.cyan(c.bold(`── theme: ${theme} ──`)))
    for (const spec of specs) {
      // Specs marked themes:false run only in the first theme to save time.
      if (!spec.opts.themes && theme !== CONFIG.themes[0]) continue
      process.stdout.write(`  ${spec.name} ${c.dim('…')}`)
      const { error, ms } = await runSpec(browser, spec, theme)
      results.push({ name: spec.name, theme, error, ms })
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
  console.log(`${c.green(`${passed} passed`)}${failed.length ? `, ${c.red(`${failed.length} failed`)}` : ''}`)
  if (failed.length) {
    for (const f of failed) console.log(c.red(`  - [${f.theme}] ${f.name}`))
    process.exit(1)
  }
}

main().catch((err) => {
  console.error('FATAL:', err)
  process.exit(1)
})
