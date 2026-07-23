import { test, goto, url, pageHeading, assert, assertVisible, settle } from '../runner.mjs'

// Every route in the local-first nav rail must load, render its own heading,
// and raise no uncaught/console errors (the runner fails the spec on those).
const ROUTES = [
  ['/dashboard', /Dashboard/i],
  ['/repos', /Repos/i],
  ['/insights', /Insights/i],
  ['/contexts', /Contexts/i],
  ['/categories', /Categories/i],
  ['/classify', /Classify/i],
  ['/taxonomy', /Taxonomy/i],
  ['/settings', /Settings/i],
]

test('shell: every nav route renders its heading', async ({ page }) => {
  for (const [route, expected] of ROUTES) {
    await goto(page, route)
    const h1 = await pageHeading(page)
    assert(expected.test(h1), `${route}: expected h1 to match ${expected}, got "${h1}"`)
  }
})

test('shell: sidebar links to every screen and marks the active one', async ({ page }) => {
  await goto(page, '/dashboard')

  // The mobile drawer is a second copy of the rail; it carries its own label so
  // the two navigation landmarks are distinguishable.
  const nav = page.locator('nav[aria-label="Primary"]')
  assert(
    (await nav.count()) === 1,
    `shell: expected exactly 1 "Primary" nav landmark, got ${await nav.count()}`,
  )
  await assertVisible(nav, 'shell: primary nav')

  for (const [route] of ROUTES) {
    await assertVisible(nav.locator(`a[href="${route}"]`), `shell: nav link for ${route}`)
  }

  // Navigating marks exactly one link active (aria-current), so the rail always
  // says where you are.
  await nav.locator('a[href="/insights"]').click()
  await page.waitForURL(/\/insights$/)
  await settle(page)
  const current = nav.locator('a[aria-current="page"]')
  const count = await current.count()
  assert(count === 1, `shell: expected exactly 1 active nav link, got ${count}`)
  const href = await current.first().getAttribute('href')
  assert(href === '/insights', `shell: active link should be /insights, got ${href}`)
})

test('shell: unknown routes fall through to a not-found page', async ({ page }) => {
  await page.goto(url('/definitely-not-a-route'), { waitUntil: 'domcontentloaded' })
  await settle(page)
  const body = (await page.locator('body').innerText()).toLowerCase()
  assert(
    /not found|404/.test(body),
    'shell: an unknown route should render a not-found page, not a blank screen',
  )
})

test('shell: legacy SaaS links redirect to their local-first equivalents', async ({ page }) => {
  // These paths were real screens in the pre-pivot SaaS app; bookmarks and old
  // README links must not dead-end.
  for (const [from, to] of [
    ['/analytics', '/insights'],
    ['/projects', '/repos'],
    ['/home', '/dashboard'],
  ]) {
    await page.goto(url(from), { waitUntil: 'domcontentloaded' })
    await page.waitForURL(new RegExp(`${to}$`), { timeout: 10_000 })
    const path = await page.evaluate(() => location.pathname)
    assert(path === to, `shell: ${from} should redirect to ${to}, landed on ${path}`)
  }
})

test('shell: theme toggle flips the document theme', async ({ page, theme }) => {
  await goto(page, '/dashboard')

  const isLight = () => page.evaluate(() => document.documentElement.classList.contains('light'))
  assert((await isLight()) === (theme === 'light'), `shell: initial theme should be ${theme}`)

  const toggle = page.locator('header button, [aria-label*="theme" i]').last()
  await toggle.click()
  await page.waitForTimeout(300)
  assert((await isLight()) !== (theme === 'light'), 'shell: toggling should flip the theme class')
})
