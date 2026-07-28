/**
 * Playwright screenshotter for gitstate.
 *
 * Captures the local-first React UI into PNGs for the README and docs. There is
 * no auth: the daemon serves the SPA and the JSON API on one origin, so every
 * route is reachable directly.
 *
 * Usage:
 *   1. Seed a demo database and start the daemon:
 *        cargo run -p gitstate-cli -- seed --demo
 *        cargo run -p gitstate-cli -- serve --port 8080
 *   2. From web/:  npm run shots
 *
 * Env:
 *   BASE_URL   default http://localhost:8080  (the daemon, which serves web/dist)
 *   OUT        default ../../docs/screenshots
 *
 * Output: $OUT/ (docs/screenshots/), referenced by the README.
 */
import { chromium } from 'playwright'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const __dirname = dirname(fileURLToPath(import.meta.url))

const BASE_URL = (process.env.BASE_URL || 'http://localhost:8080').replace(/\/$/, '')
const OUT = process.env.OUT
  ? resolve(process.env.OUT)
  : resolve(__dirname, '../../docs/screenshots')

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

let captured = []
let failed = []

async function capture(page, name) {
  const file = resolve(OUT, `${name}.png`)
  try {
    await page.screenshot({ path: file })
    captured.push(name)
    console.log(`  ✓ ${name}.png`)
  } catch (err) {
    failed.push({ name, error: String(err?.message || err) })
    console.error(`  ✗ ${name}.png — ${err?.message || err}`)
  }
}

/**
 * Scroll the app's content pane (not the window — the shell never scrolls).
 *
 * Used for the couple of screens whose second half is worth its own capture,
 * so the long ones don't have to be shot as one enormous ribbon.
 */
async function scrollMain(page, top) {
  await page.evaluate((y) => {
    const main = document.querySelector('#main-content')
    if (main) main.scrollTop = y
  }, top)
  await settle(page, { extra: 700 })
}

/**
 * Navigate to a route and capture. Waits for the charts to exist, not just the
 * heading — otherwise the shot lands mid-spinner while /api/analytics is still
 * in flight and the whole point of the capture is missing.
 */
async function shoot(page, route, name, { scroll = 0, waitFor, prepare } = {}) {
  try {
    console.log(`→ ${route}  →  ${name}.png`)
    await page.goto(`${BASE_URL}${route}`, {
      waitUntil: 'domcontentloaded',
      timeout: NAV_TIMEOUT,
    })
    try {
      await page.waitForSelector('h1', { state: 'visible', timeout: 15_000 })
    } catch {
      /* fall through — capture whatever is there */
    }
    // Some screens only show their real surface once something is picked (a
    // repo, a tab). Without this they capture as an empty state, which is a
    // misleading shot for the README and the site gallery.
    if (prepare) {
      try {
        await prepare(page)
      } catch (err) {
        console.warn(`  ⚠ prepare(${name}) — ${err?.message || err}`)
      }
    }
    if (waitFor) {
      try {
        await page.waitForSelector(waitFor, { state: 'visible', timeout: 20_000 })
      } catch {
        console.warn(`  ⚠ ${waitFor} never appeared`)
      }
    }
    await settle(page, { extra: 1200 })
    if (scroll) await scrollMain(page, scroll)
    await capture(page, name)
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
    reducedMotion: 'reduce',
  })
  context.setDefaultNavigationTimeout(NAV_TIMEOUT)
  await setTheme(context, theme)
  return context
}

/**
 * Route → output name, plus the selector that proves the data actually landed.
 * `data-chart` rather than `role="img"`: icon <svg>s carry that role too, so a
 * shot could otherwise be taken while the charts were still loading.
 */
const CHART = 'svg[data-chart]'

/**
 * Pick the first repo in the page's leading <select>. The page then hydrates
 * the labels and difficulty already stored for that repo — no classifier pass
 * is triggered, so the capture shows real state rather than an empty state.
 */
async function prepareClassify(page) {
  const select = page.locator('select').first()
  await select.waitFor({ state: 'visible', timeout: 20_000 })
  const value = await select.locator('option').nth(1).getAttribute('value')
  if (!value) return
  await select.selectOption(value)
  await page.waitForSelector('text=/difficulty|Correct label/', { timeout: 20_000 })
}

// Every screen is captured at the same 1440x900 window, the way the app
// actually looks — one consistent aspect ratio across the whole set rather
// than a few page-height ribbons. Where the interesting half sits below the
// fold, a second entry scrolls the content pane instead of stretching the shot.
const PAGES = [
  { route: '/dashboard', name: 'dashboard', waitFor: CHART },
  { route: '/insights', name: 'insights', waitFor: CHART },
  { route: '/insights', name: 'insights-trends', waitFor: CHART, scroll: 980 },
  { route: '/contribution', name: 'contribution' },
  { route: '/contribution', name: 'contribution-table', scroll: 620 },
  { route: '/eng-health', name: 'eng-health' },
  { route: '/eng-health', name: 'eng-health-quality', scroll: 520 },
  { route: '/involvement', name: 'involvement' },
  { route: '/people', name: 'people' },
  { route: '/board', name: 'board' },
  { route: '/import', name: 'import' },
  { route: '/repos', name: 'repos' },
  { route: '/contexts', name: 'contexts' },
  { route: '/categories', name: 'categories' },
  { route: '/classify', name: 'classify', prepare: prepareClassify },
  { route: '/taxonomy', name: 'taxonomy' },
  { route: '/settings', name: 'settings' },
]

async function main() {
  await mkdir(OUT, { recursive: true })
  console.log(`Base URL: ${BASE_URL}`)
  console.log(`Output:   ${OUT}\n`)

  const browser = await chromium.launch()

  // Dark is the product's default look; every screen gets a dark capture.
  {
    const ctx = await newContext(browser, 'dark')
    const page = await ctx.newPage()
    for (const p of PAGES) {
      await shoot(page, p.route, p.name, {
        scroll: p.scroll,
        waitFor: p.waitFor,
        prepare: p.prepare,
      })
    }
    // The first repo's detail page — reached by clicking through, since its id
    // is a generated uuid rather than a fixed route.
    try {
      await page.goto(`${BASE_URL}/repos`, { waitUntil: 'domcontentloaded' })
      await settle(page)
      const link = page.locator('a[href^="/repos/"], button', { hasText: /demo-org\// }).first()
      if (await link.count()) {
        await link.click()
        await page.waitForURL(/\/repos\/.+/, { timeout: 15_000 })
        await page.waitForSelector(CHART, { state: 'visible', timeout: 20_000 })
        await settle(page, { extra: 1200 })
        // Viewport only: the work-item list runs to dozens of rows, and a
        // grown window would turn the shot into a thin unreadable ribbon.
        await capture(page, 'repo-detail')
      }
    } catch (err) {
      failed.push({ name: 'repo-detail', error: String(err?.message || err) })
      console.error(`  ✗ repo-detail — ${err?.message || err}`)
    }
    await ctx.close()
  }

  // Light theme — the two screens that carry the most chrome.
  {
    const ctx = await newContext(browser, 'light')
    const page = await ctx.newPage()
    await shoot(page, '/dashboard', 'dashboard-light', { waitFor: CHART })
    await shoot(page, '/insights', 'insights-light', { waitFor: CHART })
    await ctx.close()
  }

  await browser.close()

  console.log('\n──────── summary ────────')
  console.log(`captured (${captured.length}): ${captured.join(', ')}`)
  if (failed.length) {
    console.log(`\nfailed (${failed.length}):`)
    for (const f of failed) console.log(`  - ${f.name}: ${f.error}`)
    process.exitCode = 1
  } else {
    console.log('failed: none')
  }
}

main().catch((err) => {
  console.error('FATAL:', err)
  process.exit(1)
})
