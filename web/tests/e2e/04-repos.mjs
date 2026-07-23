import {
  test, goto, api, pageHeading, assert, assertVisible, assertCountAtLeast,
} from '../runner.mjs'

const TREND = 'svg[data-chart="trend"]'
const HEATMAP = 'svg[data-chart="heatmap"]'

test('repos: the list shows every registered repo', async ({ page }) => {
  await goto(page, '/repos')

  const h1 = await pageHeading(page)
  assert(/Repos/i.test(h1), `repos: expected "Repos", got "${h1}"`)

  const repos = await api('/api/repos')
  assert(repos.length > 0, 'repos: seeded data should register repos')
  for (const repo of repos) {
    await assertVisible(page.getByText(repo.slug, { exact: true }).first(), `repos: "${repo.slug}"`)
  }
})

test('repo detail: project state, activity charts and work items all render', async ({ page }) => {
  const repos = await api('/api/repos')
  const repo = repos[0]
  await goto(page, `/repos/${repo.id}`, { waitFor: TREND })

  const h1 = await pageHeading(page)
  assert(h1.includes(repo.slug), `repo detail: expected "${repo.slug}" in heading, got "${h1}"`)

  for (const name of ['Project state', 'Contribution heatmap', 'Commits per week', 'Cycle time per merged PR']) {
    await assertVisible(page.getByRole('heading', { name }), `repo detail: "${name}" section`)
  }

  // Per-repo scoping: the detail page must show THIS repo's numbers, not the
  // whole-org roll-up.
  const scoped = await api(`/api/analytics?days=365&repo_id=${encodeURIComponent(repo.id)}`)
  const all = await api('/api/analytics?days=365')
  assert(scoped.totals.repos === 1, 'repo detail: scoped analytics should report 1 repo')
  assert(
    scoped.totals.commits > 0 && scoped.totals.commits < all.totals.commits,
    'repo detail: scoped commits should be non-zero but below the org total',
  )

  // Heatmap + two trend charts, each plotting real geometry.
  await assertVisible(page.locator(HEATMAP), 'repo detail: heatmap')
  await assertCountAtLeast(page.locator(TREND), 2, 'repo detail: trend charts')
  const lines = page.locator(`${TREND} path[data-trend-line]`)
  const n = await assertCountAtLeast(lines, 2, 'repo detail: trend lines')
  for (let i = 0; i < n; i++) {
    const d = await lines.nth(i).getAttribute('d')
    assert(d && d.includes('L'), `repo detail: trend line ${i} has no geometry`)
  }

  // The heatmap must be scoped to this repo, not the org roll-up.
  const days = Number(await page.locator(HEATMAP).first().getAttribute('data-days'))
  assert(days === scoped.heatmap.length, `repo detail: heatmap covers ${days} days, API says ${scoped.heatmap.length}`)

  const state = await api(`/api/repos/${encodeURIComponent(repo.id)}/project-state`)
  await assertVisible(
    page.getByText(String(state.merged_prs), { exact: true }).first(),
    `repo detail: merged PR count ${state.merged_prs}`,
  )

  const items = await api(`/api/repos/${encodeURIComponent(repo.id)}/work-items`)
  assert(items.length > 0, 'repo detail: seeded repo should have work items')
  await assertVisible(
    page.getByText(items[0].title, { exact: false }).first(),
    'repo detail: first work item title',
  )
})

test('repo detail: contribution dimensions render per contributor', async ({ page }) => {
  const repos = await api('/api/repos')
  const repo = repos[0]
  await goto(page, `/repos/${repo.id}`, { waitFor: TREND })

  await assertVisible(
    page.getByRole('heading', { name: /^Contribution/ }),
    'repo detail: contribution section',
  )

  const contribs = await api(`/api/repos/${encodeURIComponent(repo.id)}/contributions`)
  assert(contribs.length > 0, 'repo detail: seeded repo should have derived contributions')

  // All six gaming-resistant dimensions must be columns, not just a composite.
  for (const dim of ['Shipped', 'Review', 'Effort', 'Quality', 'Ownership', 'Durability']) {
    await assertVisible(page.getByText(dim, { exact: true }).first(), `repo detail: "${dim}" column`)
  }
})
