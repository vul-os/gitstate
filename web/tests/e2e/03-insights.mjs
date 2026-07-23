import {
  test, goto, api, pageHeading, assert, assertVisible, assertCountAtLeast, settle,
} from '../runner.mjs'

const TREND = 'svg[data-chart="trend"]'
const HEATMAP = 'svg[data-chart="heatmap"]'

test('insights: the full scalar grid renders', async ({ page }) => {
  await goto(page, '/insights', { waitFor: TREND })

  const h1 = await pageHeading(page)
  assert(/Insights/i.test(h1), `insights: expected "Insights", got "${h1}"`)

  const LABELS = [
    'Commits', 'Repos', 'Contributors', 'Active days', 'Additions',
    'Deletions', 'Net lines', 'Commits / day', 'Cycle p50', 'Test-touch',
  ]
  for (const label of LABELS) {
    await assertVisible(page.locator(`[data-stat="${label}"]`), `insights: "${label}" card`)
  }

  // Cross-check a few against the API so a card can't quietly render a zero.
  const stats = await api('/api/analytics?days=365')
  const repos = await page.locator('[data-stat="Repos"] [data-stat-value]').innerText()
  assert(
    repos.trim() === String(stats.totals.repos),
    `insights: repos card reads "${repos}", API says ${stats.totals.repos}`,
  )
  const activeDays = await page.locator('[data-stat="Active days"] [data-stat-value]').innerText()
  assert(
    activeDays.trim() === String(stats.totals.active_days),
    `insights: active-days card reads "${activeDays}", API says ${stats.totals.active_days}`,
  )
})

test('insights: every chart panel plots data', async ({ page }) => {
  await goto(page, '/insights', { waitFor: TREND })

  for (const name of ['Contribution heatmap', 'Commit volume', 'Lines added', 'Cycle time', 'Throughput']) {
    await assertVisible(page.getByRole('heading', { name }), `insights: "${name}" panel`)
  }

  await assertVisible(page.locator(HEATMAP), 'insights: heatmap')
  const trends = page.locator(TREND)
  await assertCountAtLeast(trends, 4, 'insights: trend charts')

  // Every trend line must have real geometry, not a flat placeholder.
  const lines = page.locator(`${TREND} path[data-trend-line]`)
  const n = await assertCountAtLeast(lines, 5, 'insights: trend line paths')
  for (let i = 0; i < n; i++) {
    const key = await lines.nth(i).getAttribute('data-trend-line')
    const d = await lines.nth(i).getAttribute('d')
    assert(d && d.includes('L'), `insights: trend line "${key}" has no geometry`)
  }

  // Throughput is the one chart with two series sharing an axis.
  const throughput = page.locator(`${TREND}[data-series="2"]`)
  await assertVisible(throughput, 'insights: two-series throughput chart')
})

test('insights: throughput carries a legend for its two series', async ({ page }) => {
  await goto(page, '/insights', { waitFor: TREND })

  // Two series sharing one axis must never rely on colour alone for identity.
  await assertVisible(page.getByText('Merged PRs', { exact: true }), 'insights: merged-PRs legend')
  await assertVisible(page.getByText('Closed issues', { exact: true }), 'insights: closed-issues legend')
})

test('insights: the contributor table matches the API', async ({ page }) => {
  await goto(page, '/insights', { waitFor: TREND })

  const stats = await api('/api/analytics?days=365')
  const rows = page.locator('table tbody tr')
  const count = await assertCountAtLeast(rows, 1, 'insights: contributor rows')
  assert(
    count === stats.contributors.length,
    `insights: table has ${count} rows, API reports ${stats.contributors.length} contributors`,
  )

  const first = await rows.first().innerText()
  const top = stats.contributors[0]
  assert(first.includes(top.name), `insights: first row should be "${top.name}", got "${first}"`)
  assert(
    first.includes(top.commits.toLocaleString()),
    `insights: first row should show ${top.commits} commits`,
  )

  // Agent identities are labelled, not silently folded in with humans.
  if (stats.contributors.some((c) => c.is_agent)) {
    await assertVisible(page.getByText('agent', { exact: true }).first(), 'insights: agent badge')
  }
})

test('insights: the repo filter scopes every panel', async ({ page }) => {
  await goto(page, '/insights', { waitFor: TREND })

  const repos = await api('/api/repos')
  const target = repos[0]

  const all = await api('/api/analytics?days=365')
  const scoped = await api(`/api/analytics?days=365&repo_id=${encodeURIComponent(target.id)}`)
  assert(scoped.totals.repos === 1, 'insights: a scoped query should report 1 repo')
  assert(
    scoped.totals.commits < all.totals.commits,
    'insights: one repo should have fewer commits than all of them',
  )

  await page.locator('select').selectOption(target.id)
  await settle(page, { extra: 1000 })

  const reposValue = await page.locator('[data-stat="Repos"] [data-stat-value]').innerText()
  assert(reposValue.trim() === '1', `insights: scoped view should show 1 repo, card read "${reposValue}"`)

  // The contributor table must shrink to that repo's cast too.
  const rows = await page.locator('table tbody tr').count()
  assert(
    rows === scoped.contributors.length,
    `insights: scoped table has ${rows} rows, API reports ${scoped.contributors.length}`,
  )
})

test('insights: the range filter narrows the window', async ({ page }) => {
  await goto(page, '/insights', { waitFor: HEATMAP })

  const wide = Number(await page.locator(HEATMAP).first().getAttribute('data-days'))
  assert(wide === 365, `insights: default range should be 365 days, got ${wide}`)

  await page.getByRole('radio', { name: '90d' }).click()
  await settle(page, { extra: 1000 })

  const narrow = Number(await page.locator(HEATMAP).first().getAttribute('data-days'))
  assert(narrow === 90, `insights: after picking 90d the heatmap covers ${narrow} days`)

  // The range readout shows the resolved bounds the daemon chose.
  const api90 = await api('/api/analytics?days=90')
  await assertVisible(
    page.getByText(api90.range.from, { exact: false }).first(),
    `insights: range readout for ${api90.range.from}`,
  )
})
