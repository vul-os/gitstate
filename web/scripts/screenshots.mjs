/**
 * Playwright screenshotter for gitstate.
 *
 * Captures the embedded React UI into PNGs for the README and docs.
 *
 * Usage:
 *   1. Make sure the gitstate server is running (default http://localhost:8080).
 *   2. From web/:  npm run shots
 *
 * Env:
 *   BASE_URL   default http://localhost:8080
 *   OUT        default ../../docs/screenshots  (repo docs/screenshots/)
 *   EMAIL      default demo@gitstate.dev
 *   PASSWORD   default demo1234
 */
import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const __dirname = dirname(fileURLToPath(import.meta.url))

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080'
const OUT = process.env.OUT
  ? resolve(process.env.OUT)
  : resolve(__dirname, '../../docs/screenshots')
const EMAIL = process.env.EMAIL || 'demo@gitstate.dev'
const PASSWORD = process.env.PASSWORD || 'demo1234'

const THEME_KEY = 'gs-theme' // from web/src/lib/theme.jsx

const VIEWPORT = { width: 1440, height: 900 }
const NAV_TIMEOUT = 60_000

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** Set theme in localStorage BEFORE any app page loads, via an init script. */
async function setTheme(context, theme) {
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
}

/** Wait for the page to be visually ready: network idle, fonts, settle. */
async function settle(page, { extra = 600 } = {}) {
  try {
    await page.waitForLoadState('networkidle', { timeout: NAV_TIMEOUT })
  } catch {
    /* some pages keep a connection open; don't block on it */
  }
  try {
    await page.evaluate(() => document.fonts && document.fonts.ready)
  } catch {
    /* ignore */
  }
  await sleep(extra)
}

/** Scroll through the page to trigger reveal/scroll animations, then back to top. */
async function triggerReveals(page) {
  try {
    await page.evaluate(async () => {
      const step = Math.round(window.innerHeight * 0.8)
      const max = document.body.scrollHeight
      for (let y = 0; y <= max; y += step) {
        window.scrollTo(0, y)
        await new Promise((r) => setTimeout(r, 120))
      }
      window.scrollTo(0, 0)
    })
  } catch {
    /* ignore */
  }
  await sleep(500)
}

let captured = []
let failed = []

async function capture(page, name, { fullPage = false } = {}) {
  const file = resolve(OUT, `${name}.png`)
  try {
    await page.screenshot({ path: file, fullPage })
    captured.push(name)
    console.log(`  ✓ ${name}.png`)
  } catch (err) {
    failed.push({ name, error: String(err?.message || err) })
    console.error(`  ✗ ${name}.png — ${err?.message || err}`)
  }
}

/** Navigate to a route and capture; failures are isolated per page. */
async function shoot(page, route, name, { fullPage = false, reveals = false } = {}) {
  try {
    console.log(`→ ${route}  →  ${name}.png`)
    await page.goto(`${BASE_URL}${route}`, {
      waitUntil: 'domcontentloaded',
      timeout: NAV_TIMEOUT,
    })
    // Wait for real content (a heading) to be painted — guards against
    // capturing during a page-enter fade or before data resolves.
    try {
      await page.waitForSelector('h1, h2, main', { state: 'visible', timeout: 15_000 })
    } catch {
      /* fall through — capture whatever is there */
    }
    await settle(page, { extra: 1000 })
    if (reveals) {
      await triggerReveals(page)
      await settle(page, { extra: 400 })
    }
    await capture(page, name, { fullPage })
  } catch (err) {
    failed.push({ name, error: String(err?.message || err) })
    console.error(`  ✗ ${name} (nav) — ${err?.message || err}`)
  }
}

async function newContext(browser, theme) {
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: 2,
    colorScheme: theme === 'light' ? 'light' : 'dark',
    // Render scroll-reveal content immediately so full-page shots aren't blank below the fold.
    reducedMotion: 'reduce',
  })
  context.setDefaultNavigationTimeout(NAV_TIMEOUT)
  await setTheme(context, theme)
  return context
}

async function main() {
  await mkdir(OUT, { recursive: true })
  console.log(`Base URL: ${BASE_URL}`)
  console.log(`Output:   ${OUT}\n`)

  const browser = await chromium.launch()

  // ---- PUBLIC PAGES (no auth) ----
  // Dark theme
  {
    const ctx = await newContext(browser, 'dark')
    const page = await ctx.newPage()
    await shoot(page, '/', 'landing-dark', { fullPage: true, reveals: true })
    await shoot(page, '/pricing', 'pricing', { fullPage: true, reveals: true })
    await shoot(page, '/compare', 'compare', { fullPage: true, reveals: true })
    await shoot(page, '/docs', 'docs', { fullPage: true })
    await ctx.close()
  }
  // Light theme — landing (at minimum)
  {
    const ctx = await newContext(browser, 'light')
    const page = await ctx.newPage()
    await shoot(page, '/', 'landing-light', { fullPage: true, reveals: true })
    await shoot(page, '/pricing', 'pricing-light', { fullPage: true, reveals: true })
    await ctx.close()
  }

  // ---- AUTHED PAGES ----
  {
    const ctx = await newContext(browser, 'dark')
    const page = await ctx.newPage()
    let loggedIn = false
    try {
      console.log('\n→ logging in via /login')
      await page.goto(`${BASE_URL}/login`, {
        waitUntil: 'domcontentloaded',
        timeout: NAV_TIMEOUT,
      })
      await settle(page)
      await page.fill('#email', EMAIL)
      await page.fill('#password', PASSWORD)
      await Promise.all([
        page.waitForURL((url) => !/\/login$/.test(url.pathname), { timeout: NAV_TIMEOUT }),
        page.click('button[type="submit"]'),
      ])
      await settle(page)
      loggedIn = true
      console.log(`  ✓ logged in → ${page.url()}`)
    } catch (err) {
      failed.push({ name: 'login', error: String(err?.message || err) })
      console.error(`  ✗ login failed — ${err?.message || err}`)
    }

    if (loggedIn) {
      await shoot(page, '/dashboard', 'dashboard', { reveals: true })
      await shoot(page, '/board', 'board', { reveals: true })
      await shoot(page, '/involvement', 'involvement', { reveals: true })
      await shoot(page, '/capacity', 'capacity', { reveals: true })
      await shoot(page, '/cycle-time', 'cycle-time', { reveals: true })
      await shoot(page, '/settings/billing', 'billing', { reveals: true })
    } else {
      for (const n of ['dashboard', 'board', 'involvement', 'capacity', 'cycle-time', 'billing']) {
        failed.push({ name: n, error: 'skipped — login failed' })
      }
    }
    await ctx.close()
  }

  await browser.close()

  console.log('\n──────── summary ────────')
  console.log(`captured (${captured.length}): ${captured.join(', ')}`)
  if (failed.length) {
    console.log(`\nfailed (${failed.length}):`)
    for (const f of failed) console.log(`  - ${f.name}: ${f.error}`)
  } else {
    console.log('failed: none')
  }
}

main().catch((err) => {
  console.error('FATAL:', err)
  process.exit(1)
})
