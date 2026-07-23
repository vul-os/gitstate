import {
  test, goto, api, pageHeading, assert, assertVisible, assertCountAtLeast, settle,
} from '../runner.mjs'

// Charts carry `data-chart`; icon <svg>s in the icon set also use role="img",
// so structural selectors alone would match them too.
const TREND = 'svg[data-chart="trend"]'
const HEATMAP = 'svg[data-chart="heatmap"]'

test('dashboard: headline stat cards carry real values', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: TREND })

  const h1 = await pageHeading(page)
  assert(/Dashboard/i.test(h1), `dashboard: expected "Dashboard", got "${h1}"`)

  const stats = await api('/api/analytics?days=180')

  for (const label of ['Commits', 'Merged PRs', 'Cycle p50', 'Contributors']) {
    await assertVisible(page.locator(`[data-stat="${label}"]`), `dashboard: "${label}" stat card`)
  }

  // Values must come from the API, not from a hardcoded or stale render.
  const contributors = await page
    .locator('[data-stat="Contributors"] [data-stat-value]')
    .innerText()
  assert(
    contributors.trim() === String(stats.totals.contributors),
    `dashboard: contributors card reads "${contributors}", API says ${stats.totals.contributors}`,
  )

  const merged = await page.locator('[data-stat="Merged PRs"] [data-stat-value]').innerText()
  assert(
    merged.trim() === String(stats.totals.merged_prs),
    `dashboard: merged-PRs card reads "${merged}", API says ${stats.totals.merged_prs}`,
  )

  assert(stats.totals.commits > 0, 'dashboard: seeded data should have commits')
})

test('dashboard: cycle-time trend draws a real line', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: TREND })

  await assertVisible(
    page.getByRole('heading', { name: 'Cycle time trend' }),
    'dashboard: cycle-time heading',
  )

  const line = page.locator(`${TREND} path[data-trend-line="cycle"]`).first()
  await assertVisible(line, 'dashboard: cycle-time line path')
  const d = await line.getAttribute('d')
  assert(d && d.includes('L'), `dashboard: cycle-time line has no geometry (d="${d}")`)
  const vertices = (d.match(/L/g) || []).length
  assert(vertices > 5, `dashboard: cycle-time line has only ${vertices} segments`)
})

test('dashboard: heatmap renders a dense, zero-filled grid', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: HEATMAP })

  await assertVisible(
    page.getByRole('heading', { name: 'Contribution heatmap' }),
    'dashboard: heatmap heading',
  )

  const stats = await api('/api/analytics?days=180')
  const svg = page.locator(HEATMAP).first()
  const declared = Number(await svg.getAttribute('data-days'))
  assert(
    declared === stats.heatmap.length,
    `dashboard: heatmap says ${declared} days, API returned ${stats.heatmap.length}`,
  )

  // One cell per day in range, including the silent ones — that density is what
  // makes the calendar readable.
  const cells = svg.locator('rect[data-day]')
  const count = await cells.count()
  assert(
    count === stats.heatmap.length,
    `dashboard: heatmap drew ${count} cells for ${stats.heatmap.length} days`,
  )

  // Cells must span several ramp steps, or the magnitude encoding is dead.
  const levels = await cells.evaluateAll((nodes) =>
    Array.from(new Set(nodes.map((n) => n.getAttribute('data-level')))).sort(),
  )
  assert(levels.length >= 4, `dashboard: heatmap used only ${levels.length} ramp steps: ${levels}`)
  assert(levels.includes('0'), 'dashboard: quiet days should render the empty well (level 0)')
})

test('dashboard: contributor leaderboard is populated and ordered', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: TREND })

  const stats = await api('/api/analytics?days=180')
  const top = stats.contributors[0]
  assert(top, 'dashboard: seeded data should have contributors')

  await assertVisible(
    page.getByRole('heading', { name: 'Top contributors' }),
    'dashboard: leaderboard heading',
  )
  // The API orders by commits desc; the first row must be the same person.
  const firstRow = page.locator('ol li').first()
  const text = await firstRow.innerText()
  assert(
    text.includes(top.name) || text.includes(top.email),
    `dashboard: leaderboard should lead with "${top.name}", got "${text}"`,
  )
  assert(
    text.includes(top.commits.toLocaleString()),
    `dashboard: leaderboard should show ${top.commits} commits, got "${text}"`,
  )
})

test('dashboard: the range filter re-queries the daemon', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: TREND })

  const requests = []
  page.on('request', (r) => {
    if (r.url().includes('/api/analytics')) requests.push(r.url())
  })

  await page.getByRole('radio', { name: '30d' }).click()
  await settle(page, { extra: 900 })

  assert(
    requests.some((u) => /days=30\b/.test(u)),
    `dashboard: expected a days=30 request, saw ${JSON.stringify(requests)}`,
  )

  // And the narrower window must actually shrink the rendered grid.
  const declared = Number(await page.locator(HEATMAP).first().getAttribute('data-days'))
  assert(declared === 30, `dashboard: after picking 30d the heatmap covers ${declared} days`)
})

test('dashboard: every tracked repo is listed and links to its detail page', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: TREND })

  const repos = await api('/api/repos')
  for (const repo of repos) {
    await assertVisible(page.getByText(repo.slug, { exact: true }).first(), `dashboard: repo "${repo.slug}"`)
  }
  await assertCountAtLeast(page.getByText(/^demo-org\//), repos.length, 'dashboard: repo rows')

  await page.getByText(repos[0].slug, { exact: true }).first().click()
  await page.waitForURL(/\/repos\/.+/, { timeout: 15_000 })
  const path = await page.evaluate(() => location.pathname)
  assert(path.startsWith('/repos/'), `dashboard: expected a repo detail route, got ${path}`)
})
