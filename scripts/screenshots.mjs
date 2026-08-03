#!/usr/bin/env node
/**
 * gitstate screenshot generator, for README.md and site/screenshots/.
 *
 * Captures REAL UI (Playwright/Chromium, 1440-wide, every screen in BOTH
 * colour schemes — light keeps the bare name, dark gets a -dark suffix)
 * of the actual Rust daemon (`gitstate serve`) serving the built React app
 * (`web/dist`) against a deterministic, fully-synthetic demo database
 * (`gitstate seed --demo`) — a fake org, fake pseudonymous contributors
 * (`@example.com`), never real git/forge data. See
 * crates/gitstate-cli/src/cmd/seed.rs for exactly what gets seeded.
 *
 * Run FORGE-DISABLED, always: this script strips every gh/glab/LLM token
 * (GH_TOKEN, GITHUB_TOKEN, GITSTATE_GH_TOKEN, GITLAB_TOKEN, GL_TOKEN,
 * GITSTATE_GLAB_TOKEN, OPENAI_API_KEY, OPENAI_BASE_URL, VULOS_LLMUX_URL,
 * VULOS_LLMUX_API_KEY, GITSTATE_CLASSIFY_API_KEY) from the daemon's
 * environment before spawning it, and never clicks "Scan" (which is the only
 * UI action that would shell out to gh/glab) or anything that would need a
 * real forge login. The one classify call this script does make (on the
 * /classify screen, to populate a non-empty screenshot) resolves to the
 * built-in deterministic heuristic classifier — see
 * gitstate_classify::classifier(), which only ever constructs the LLM path
 * when an API key/base-url is present — so it never touches the network
 * either.
 *
 * Usage:
 *   npx playwright install chromium   # one-time, if not already cached
 *   node scripts/screenshots.mjs
 *
 * What this script does (and tears down again on exit):
 *   1. Builds `target/release/gitstate` if it isn't already built.
 *   2. Seeds a throwaway SQLite db (scripts/.screenshot-data/gitstate.db)
 *      with `gitstate seed --demo`.
 *   3. Starts `gitstate serve` against that db + web/dist on a local port
 *      not likely to collide with a real dev instance, waits for /health.
 *   4. Drives it with Playwright and writes PNGs to docs/screenshots/,
 *      mirrored into site/screenshots/.
 *   5. Kills the daemon it started.
 *
 * Reuse: if something is already answering /health on the configured port,
 * this script reuses it as-is (no build, no seed, no spawn) — point
 * GITSTATE_SCREENSHOT_PORT at your own already-running `gitstate serve` to
 * skip the bootstrap entirely. Only what THIS run started gets torn down.
 *
 * macOS dylib note: on some machines `cargo build --release` here links
 * `libiconv`/`libz` via a bare `@rpath` entry with no LC_RPATH (seen on this
 * dev machine — a stray conda toolchain on PATH at build time, unrelated to
 * gitstate's own build config) and the binary refuses to dyld-load. This
 * script detects that failure mode specifically and retries once with
 * DYLD_FALLBACK_LIBRARY_PATH pointed at the first place it can actually find
 * the missing dylib (Homebrew, conda, /usr/lib, ...) rather than silently
 * masking other launch failures.
 */

import { execFileSync, spawn } from 'node:child_process'
import { createRequire } from 'node:module'
import {
  existsSync, mkdirSync, rmSync, readdirSync, copyFileSync, statSync,
} from 'node:fs'
import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { platform } from 'node:os'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(__dirname, '..')

const BIN = join(ROOT, 'target', 'release', 'gitstate')
const WEB_DIST = join(ROOT, 'web', 'dist')
const DATA_DIR = join(ROOT, 'scripts', '.screenshot-data')
const DB_PATH = join(DATA_DIR, 'gitstate.db')

const OUT_DIRS = [join(ROOT, 'docs', 'screenshots'), join(ROOT, 'site', 'screenshots')]

const PORT = process.env.GITSTATE_SCREENSHOT_PORT || '8791'
const BASE_URL = `http://127.0.0.1:${PORT}`

const VIEWPORT = { width: 1440, height: 900 }
const DEVICE_SCALE = 2
const NAV_TIMEOUT = 20_000
const THEME_KEY = 'gs-theme' // web/src/lib/theme.jsx

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// Forge/LLM env vars this script refuses to let the daemon see. See
// crates/gitstate-forge/src/{github,gitlab}.rs and
// crates/gitstate-classify/src/llm.rs for exactly what each one gates.
const STRIP_ENV_KEYS = [
  'GH_TOKEN', 'GITHUB_TOKEN', 'GITSTATE_GH_TOKEN',
  'GITLAB_TOKEN', 'GL_TOKEN', 'GITSTATE_GLAB_TOKEN',
  'OPENAI_API_KEY', 'OPENAI_BASE_URL',
  'VULOS_LLMUX_URL', 'VULOS_LLMUX_API_KEY', 'GITSTATE_CLASSIFY_API_KEY',
]

function forgeDisabledEnv() {
  const env = { ...process.env }
  for (const k of STRIP_ENV_KEYS) delete env[k]
  return env
}

// ---------------------------------------------------------------------------
// Playwright — loaded from web/node_modules (no root package.json here; the
// desktop web app already depends on playwright for its own tests/shots).
// ---------------------------------------------------------------------------
const require_ = createRequire(import.meta.url)
const playwrightPkg = join(ROOT, 'web', 'node_modules', 'playwright')
if (!existsSync(playwrightPkg)) {
  throw new Error(
    `playwright not found at ${playwrightPkg} — run "npm install" in web/ first ` +
      '(it is already a devDependency there), then "npx playwright install chromium".',
  )
}
const { chromium } = require_(playwrightPkg)

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------
function run(cmd, args, opts = {}) {
  execFileSync(cmd, args, { stdio: 'inherit', ...opts })
}

// Like run(), but captures stderr (in addition to still printing it) so
// callers can pattern-match the dylib-retry condition on failure — plain
// stdio:'inherit' doesn't populate err.stderr on a thrown error.
function runCapturing(cmd, args, opts = {}) {
  try {
    const out = execFileSync(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], ...opts })
    process.stdout.write(out)
  } catch (err) {
    if (err.stdout) process.stdout.write(err.stdout)
    if (err.stderr) process.stderr.write(err.stderr)
    throw err
  }
}

function ensureBinary() {
  if (existsSync(BIN)) {
    console.log(`  reusing ${BIN}`)
    return
  }
  console.log('  building gitstate-cli (release)…')
  run('cargo', ['build', '--release', '-p', 'gitstate-cli'], { cwd: ROOT })
}

// ---------------------------------------------------------------------------
// macOS-only: work around a bare-@rpath dylib link with no LC_RPATH by
// finding a real directory that has the missing library and passing it via
// DYLD_FALLBACK_LIBRARY_PATH. No-op (and harmless) everywhere else.
// ---------------------------------------------------------------------------
function dyldFallbackCandidateDirs() {
  const dirs = [
    '/opt/homebrew/lib',
    '/usr/local/lib',
    '/usr/lib',
  ]
  if (process.env.CONDA_PREFIX) dirs.push(join(process.env.CONDA_PREFIX, 'lib'))
  // Common conda/anaconda install locations, if present, even with no active env.
  dirs.push('/opt/homebrew/anaconda3/lib', join(process.env.HOME || '', 'miniconda3', 'lib'))
  return dirs.filter((d) => existsSync(d))
}

function looksLikeMissingRpathDylib(stderrText) {
  return /Library not loaded: @rpath\/.*\.dylib/.test(stderrText)
    && /no LC_RPATH's found/.test(stderrText)
}

function withDyldFallback(env) {
  if (platform() !== 'darwin') return env
  const dirs = dyldFallbackCandidateDirs()
  if (!dirs.length) return env
  const existing = env.DYLD_FALLBACK_LIBRARY_PATH ? [env.DYLD_FALLBACK_LIBRARY_PATH] : []
  return { ...env, DYLD_FALLBACK_LIBRARY_PATH: [...dirs, ...existing].join(':') }
}

// ---------------------------------------------------------------------------
// Seed
// ---------------------------------------------------------------------------
function seedDemoDb() {
  rmSync(DATA_DIR, { recursive: true, force: true })
  mkdirSync(DATA_DIR, { recursive: true })
  console.log(`  seeding synthetic demo data -> ${DB_PATH}`)
  try {
    runCapturing(BIN, ['seed', '--demo', '--db', DB_PATH], { env: forgeDisabledEnv() })
  } catch (err) {
    const stderr = String(err.stderr || err.message || '')
    if (looksLikeMissingRpathDylib(stderr)) {
      console.log('  (retrying seed with DYLD_FALLBACK_LIBRARY_PATH — see file header)')
      runCapturing(BIN, ['seed', '--demo', '--db', DB_PATH], { env: withDyldFallback(forgeDisabledEnv()) })
      return
    }
    throw err
  }
}

// ---------------------------------------------------------------------------
// Daemon
// ---------------------------------------------------------------------------
async function health() {
  try {
    const res = await fetch(`${BASE_URL}/health`)
    if (!res.ok) return null
    return await res.json().catch(() => ({}))
  } catch {
    return null
  }
}

// Returns { proc, logs, hasExited() } — `hasExited()` is a real boolean.
// (proc.on('exit', code => ...) fires with code === null when the process
// died from a signal — e.g. a dyld load failure aborts via SIGABRT — so
// tracking only the exit *code* is indistinguishable from "still running".)
function spawnDaemon(env) {
  const proc = spawn(
    BIN,
    ['serve', '--addr', '127.0.0.1', '--port', PORT, '--web-dist', WEB_DIST],
    { cwd: ROOT, env: { ...env, GITSTATE_DATA_DIR: DATA_DIR }, stdio: ['ignore', 'pipe', 'pipe'] },
  )
  const logs = []
  proc.stdout.on('data', (d) => logs.push(String(d)))
  proc.stderr.on('data', (d) => logs.push(String(d)))
  let exited = false
  let exitInfo = ''
  proc.on('exit', (code, signal) => { exited = true; exitInfo = `code ${code}, signal ${signal}` })
  return { proc, logs, hasExited: () => exited, exitInfo: () => exitInfo }
}

async function ensureDaemon() {
  const already = await health()
  if (already?.status === 'ok') {
    console.log(`  reusing running gitstate daemon at ${BASE_URL} (v${already.version})`)
    return { teardown: async () => {} }
  }

  const baseEnv = forgeDisabledEnv()
  let { proc, logs, hasExited, exitInfo } = spawnDaemon(baseEnv)

  // Give it a moment (polling, since the dyld crash can take a beat to
  // surface as a Node 'exit' event), then check for the known dylib failure
  // mode and retry once with the fallback library path before giving up.
  const earlyDeadline = Date.now() + 1500
  while (!hasExited() && Date.now() < earlyDeadline) await sleep(50)
  if (hasExited() && looksLikeMissingRpathDylib(logs.join(''))) {
    console.log('  (retrying serve with DYLD_FALLBACK_LIBRARY_PATH — see file header)')
    ;({ proc, logs, hasExited, exitInfo } = spawnDaemon(withDyldFallback(baseEnv)))
  }

  const deadline = Date.now() + NAV_TIMEOUT
  while (Date.now() < deadline) {
    if (hasExited()) throw new Error(`gitstate serve exited early (${exitInfo()}):\n${logs.join('')}`)
    if ((await health())?.status === 'ok') break
    await sleep(200)
  }
  if ((await health())?.status !== 'ok') {
    proc.kill('SIGTERM')
    throw new Error(`gitstate serve did not become ready on ${BASE_URL}:\n${logs.join('')}`)
  }
  console.log(`  gitstate serve up on ${BASE_URL}`)

  const teardown = async () => {
    proc.kill('SIGTERM')
    const stopDeadline = Date.now() + 5000
    while (!hasExited() && Date.now() < stopDeadline) await sleep(25)
    if (!hasExited()) proc.kill('SIGKILL')
  }
  return { teardown }
}

// ---------------------------------------------------------------------------
// Capture helpers
// ---------------------------------------------------------------------------
async function setTheme(context, theme) {
  await context.addInitScript((v) => {
    try { window.localStorage.setItem(v.key, v.theme) } catch { /* ignore */ }
  }, { key: THEME_KEY, theme })
}

async function settle(page, extra = 400) {
  try { await page.waitForLoadState('networkidle', { timeout: NAV_TIMEOUT }) } catch { /* ignore */ }
  try { await page.evaluate(() => document.fonts && document.fonts.ready) } catch { /* ignore */ }
  await sleep(extra)
}

async function firstRepoId() {
  const res = await fetch(`${BASE_URL}/api/repos`)
  const repos = await res.json()
  if (!repos?.length) throw new Error('no repos in the seeded demo db — did `gitstate seed --demo` run?')
  // atlas-api if present (first seeded repo), else whatever sorts first.
  const atlas = repos.find((r) => r.slug.endsWith('/atlas-api'))
  return (atlas || repos[0]).id
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
async function main() {
  console.log('\ngitstate screenshotter')
  console.log(`  BASE_URL : ${BASE_URL}`)
  console.log(`  output   : ${OUT_DIRS.join(', ')}\n`)

  ensureBinary()

  let daemon
  let seeded = false
  const already = await health()
  if (!(already?.status === 'ok')) {
    seedDemoDb()
    seeded = true
  } else {
    console.log('  daemon already running — reusing its data as-is (not reseeding)')
  }

  daemon = await ensureDaemon()
  try {
    const repoId = await firstRepoId()
    console.log(`  using repo ${repoId} for the per-repo screens`)

    for (const dir of OUT_DIRS) mkdirSync(dir, { recursive: true })

    const browser = await chromium.launch({ headless: true })
    try {
      // ---- one pass per colour scheme -------------------------------
      // Every screen is captured in BOTH themes, not just the dashboard. The
      // landing page is dark, and it was showing the light captures: a white
      // dashboard dropped into a near-black page. Whichever theme a page ends
      // up presenting, the matching file now exists.
      //
      // Dark files carry a "-dark" suffix; light keeps the bare name, so every
      // existing reference to dashboard.png/classify.png still resolves.
      const capture = async (theme) => {
        const suffix = theme === 'dark' ? '-dark' : ''
        const shot = (name, opts) => join(OUT_DIRS[0], `${name}${suffix}.png`)
        const ctx = await browser.newContext({
          viewport: VIEWPORT, deviceScaleFactor: DEVICE_SCALE, colorScheme: theme,
        })
        await setTheme(ctx, theme)
        const page = await ctx.newPage()

        // Dashboard — cross-repo overview (the hero).
        await page.goto(`${BASE_URL}/dashboard`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('dashboard') })
        console.log(`  ✓ dashboard${suffix}.png`)

        // The remaining screens the landing's gallery names. These are plain
        // route visits with no interaction; grouping them keeps the two passes
        // symmetrical, which is the whole point — a screen with only a light
        // capture is a screen the dark landing has to show in the wrong theme.
        for (const [name, route] of [
          ['board',        '/board'],
          ['repos',        '/repos'],
          ['people',       '/people'],
          ['involvement',  '/involvement'],
          ['taxonomy',     '/taxonomy'],
          ['settings',     '/settings'],
          ['contribution', '/contribution'],
        ]) {
          const res = await page.goto(`${BASE_URL}${route}`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
          if (res && res.status() >= 400) { console.warn(`  ! skipped ${name}${suffix}.png — ${route} returned ${res.status()}`); continue }
          await settle(page)
          await page.screenshot({ path: shot(name) })
          console.log(`  ✓ ${name}${suffix}.png`)
        }

        // Project state + six-dimension contribution — one repo, full page.
        await page.goto(`${BASE_URL}/repos/${repoId}`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('project-state'), fullPage: true })
        console.log(`  ✓ project-state${suffix}.png`)

        // Dedicated close-up on the contribution table (six dimension bars).
        const contribHeading = page.getByText('Contribution', { exact: false }).first()
        await contribHeading.scrollIntoViewIfNeeded()
        await sleep(200)
        await page.screenshot({ path: shot('contributions') })
        console.log(`  ✓ contributions${suffix}.png`)

        // Contexts — saved working sets.
        await page.goto(`${BASE_URL}/contexts`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('contexts') })
        console.log(`  ✓ contexts${suffix}.png`)

        // Categories — the local taxonomy.
        await page.goto(`${BASE_URL}/categories`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('categories') })
        console.log(`  ✓ categories${suffix}.png`)

        // Eng Health — the derived quality view.
        await page.goto(`${BASE_URL}/eng-health`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('eng-health') })
        console.log(`  ✓ eng-health${suffix}.png`)

        // Insights — trends across every tracked repo.
        await page.goto(`${BASE_URL}/insights`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('insights') })
        console.log(`  ✓ insights${suffix}.png`)

        // Import — bringing repos in.
        await page.goto(`${BASE_URL}/import`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.screenshot({ path: shot('import') })
        console.log(`  ✓ import${suffix}.png`)

        // Classify — pick the repo, run the (heuristic, offline) classifier.
        await page.goto(`${BASE_URL}/classify`, { waitUntil: 'networkidle', timeout: NAV_TIMEOUT })
        await settle(page)
        await page.locator('select').first().selectOption(repoId)
        await sleep(400)
        const classifyBtn = page.getByRole('button', { name: /classify items/i })
        if (await classifyBtn.count()) {
          await classifyBtn.click()
          await sleep(600)
        }
        await page.screenshot({ path: shot('classify') })
        console.log(`  ✓ classify${suffix}.png`)

        await ctx.close()
      }

      for (const theme of ['light', 'dark']) {
        console.log(`\n  ── ${theme} pass ──`)
        await capture(theme)
      }

    } finally {
      await browser.close()
    }

    // Mirror everything into site/screenshots/.
    const files = readdirSync(OUT_DIRS[0]).filter((f) => f.endsWith('.png'))
    for (const f of files) copyFileSync(join(OUT_DIRS[0], f), join(OUT_DIRS[1], f))
    console.log(`\nMirrored ${files.length} screenshots into ${OUT_DIRS[1]}`)

    for (const f of files) {
      const { size } = statSync(join(OUT_DIRS[0], f))
      console.log(`  ${f.padEnd(24)} ${(size / 1024).toFixed(0)} KB`)
    }

    console.log('\nDone.')
  } finally {
    await daemon.teardown()
    if (seeded) console.log('  (daemon stopped; seeded demo db left at scripts/.screenshot-data/ for reuse)')
  }
}

main().catch((err) => {
  console.error('\nscreenshotter error:', err.message)
  process.exit(1)
})
