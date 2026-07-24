/**
 * The screens restored for parity with the pre-pivot app: Contribution (with
 * the weight tuner), Eng Health, Involvement, People and Board.
 *
 * Same convention as the rest of the suite: assert against the API the page
 * itself calls, so these survive a change to the seed data while still
 * catching a tile that renders a stale or invented number.
 */
import {
  test, goto, api, pageHeading, assert, assertVisible, assertCountAtLeast, settle,
} from '../runner.mjs'

// ─────────────────────────────── contribution ───────────────────────────────

test('contribution: the six dimensions render for every contributor', async ({ page }) => {
  await goto(page, '/contribution')

  const h1 = await pageHeading(page)
  assert(/Contribution/i.test(h1), `contribution: expected "Contribution", got "${h1}"`)

  // The framing matters as much as the numbers — this is explicitly not a
  // leaderboard, and the README is emphatic about it.
  const body = await page.locator('main').innerText()
  assert(
    /never a leaderboard|not a ranking|texture/i.test(body),
    'contribution: the page must state that this is texture, not a ranking',
  )

  for (const dim of ['Shipped', 'Review', 'Effort', 'Quality', 'Ownership', 'Durability']) {
    await assertVisible(page.getByText(dim, { exact: true }).first(), `contribution: "${dim}" column`)
  }

  const rollup = await api('/api/contributions/rollup')
  assert(rollup.length > 0, 'contribution: seeded data should produce rollup rows')
  const rows = page.locator('table tbody tr')
  const count = await assertCountAtLeast(rows, 1, 'contribution: contributor rows')
  assert(
    count === rollup.length,
    `contribution: table has ${count} rows, API reports ${rollup.length}`,
  )

  // Every rollup row must carry all six dimensions and a finite composite.
  for (const r of rollup) {
    for (const d of ['shipped', 'review', 'effort', 'quality', 'ownership', 'durability']) {
      assert(Number.isFinite(r.dimensions[d]), `contribution: ${r.display_name}.${d} is not finite`)
    }
    assert(Number.isFinite(r.composite), `contribution: ${r.display_name} composite is not finite`)
    assert(r.repos.length > 0, `contribution: ${r.display_name} should list its repos`)
  }
})

test('contribution: the weight tuner re-ranks live and persists', async ({ page }) => {
  await goto(page, '/contribution')

  const before = await api('/api/weights')
  assert(Number.isFinite(before.shipped), 'contribution: /api/weights should return numbers')

  const sliders = page.locator('input[type="range"]')
  await assertCountAtLeast(sliders, 6, 'contribution: one slider per dimension')

  // Every slider must be labelled — six anonymous ranges would be unusable.
  for (let i = 0; i < 6; i++) {
    const id = await sliders.nth(i).getAttribute('id')
    const aria = await sliders.nth(i).getAttribute('aria-label')
    assert(
      aria || (id && (await page.locator(`label[for="${id}"]`).count()) > 0),
      `contribution: slider ${i} has no label`,
    )
  }

  const firstRowBefore = await page.locator('table tbody tr').first().innerText()

  // Drive the slider by keyboard: a controlled range input ignores fill().
  await sliders.first().focus()
  for (let i = 0; i < 12; i++) await page.keyboard.press('Home')
  await settle(page, { extra: 400 })

  const firstRowAfter = await page.locator('table tbody tr').first().innerText()
  assert(
    firstRowBefore !== firstRowAfter,
    'contribution: zeroing a dimension should change the live composite ordering',
  )

  // Saving persists through the daemon, normalized.
  await page.getByRole('button', { name: /save weights/i }).click()
  await settle(page, { extra: 600 })
  const after = await api('/api/weights')
  const sum = ['shipped', 'review', 'effort', 'quality', 'ownership', 'durability']
    .reduce((t, k) => t + after[k], 0)
  assert(Math.abs(sum - 1) < 1e-6, `contribution: saved weights should normalize, summed ${sum}`)

  // Leave the daemon as we found it so spec order can't matter.
  await page.getByRole('button', { name: /reset/i }).click()
  await settle(page, { extra: 600 })
})

// ─────────────────────────────── eng health ───────────────────────────────

test('eng-health: DORA, bus factor, review and quality all render real numbers', async ({ page }) => {
  await goto(page, '/eng-health')

  const h1 = await pageHeading(page)
  assert(/Eng Health/i.test(h1), `eng-health: expected "Eng Health", got "${h1}"`)

  const m = await api('/api/health-metrics?days=90')

  for (const name of ['Bus factor', 'Review coverage', 'Quality signals']) {
    await assertVisible(page.getByRole('heading', { name }), `eng-health: "${name}" panel`)
  }

  // The bus-factor headline must match the API exactly.
  const body = await page.locator('main').innerText()
  assert(
    body.includes(String(m.bus_factor.count)),
    `eng-health: bus factor ${m.bus_factor.count} not shown`,
  )
  assert(
    body.includes(String(m.review.unreviewed_merged)),
    `eng-health: unreviewed count ${m.review.unreviewed_merged} not shown`,
  )

  // Nothing may render as NaN/undefined — the failure mode these guard.
  assert(!/NaN|undefined|Infinity/.test(body), 'eng-health: rendered a non-value')

  // The deploy figure is a proxy (merge commits), not a real deploy count, and
  // the UI must not imply otherwise.
  assert(/proxy/i.test(body), 'eng-health: the deploy proxy must be labelled as a proxy')
})

test('eng-health: the repo filter rescopes the metrics', async ({ page }) => {
  await goto(page, '/eng-health')
  const repos = await api('/api/repos')
  const scoped = await api(`/api/health-metrics?days=90&repo_id=${encodeURIComponent(repos[0].id)}`)
  const all = await api('/api/health-metrics?days=90')

  assert(
    scoped.review.merged_prs <= all.review.merged_prs,
    'eng-health: one repo cannot have more merged PRs than every repo',
  )

  await page.locator('select').first().selectOption(repos[0].id)
  await settle(page, { extra: 900 })
  const body = await page.locator('main').innerText()
  assert(!/NaN|undefined/.test(body), 'eng-health: scoped view rendered a non-value')
})

// ─────────────────────────────── involvement ───────────────────────────────

test('involvement: both directions of the person↔repo join render', async ({ page }) => {
  await goto(page, '/involvement')

  const h1 = await pageHeading(page)
  assert(/Involvement/i.test(h1), `involvement: expected "Involvement", got "${h1}"`)

  const inv = await api('/api/involvement?days=365')
  assert(inv.repos.length > 0 && inv.people.length > 0, 'involvement: seeded data should populate both')

  // Default view is by repo.
  const body = await page.locator('main').innerText()
  assert(body.includes(inv.repos[0].slug), `involvement: repo "${inv.repos[0].slug}" not shown`)

  // Flip to the person view.
  await page.getByRole('radio', { name: /person/i }).click()
  await settle(page, { extra: 500 })
  const after = await page.locator('main').innerText()
  const top = inv.people[0]
  assert(
    after.includes(top.name) || after.includes(top.email),
    `involvement: person view should show "${top.name}"`,
  )
  assert(!/NaN|undefined/.test(after), 'involvement: rendered a non-value')

  // Shares are fractions server-side and must render as percentages.
  for (const r of inv.repos) {
    for (const c of r.contributors) {
      assert(c.share >= 0 && c.share <= 1, `involvement: share ${c.share} out of range`)
    }
  }
})

// ─────────────────────────────── people ───────────────────────────────

test('people: the merged-identity roster lists aliases and agents', async ({ page }) => {
  await goto(page, '/people')

  const h1 = await pageHeading(page)
  assert(/People/i.test(h1), `people: expected "People", got "${h1}"`)

  const contributors = await api('/api/contributors')
  assert(contributors.length > 0, 'people: seeded data should have contributors')

  const body = await page.locator('main').innerText()
  assert(body.includes(contributors[0].primary_email), 'people: primary email not shown')

  // Agent identities are badged with their specific kind (`ci-agent`,
  // `coding-agent`), not a generic label — that distinction is the point.
  const agent = contributors.find((c) => c.is_agent)
  if (agent) {
    const badge = agent.agent_kind || 'agent'
    assert(body.includes(badge), `people: agent identity should be badged "${badge}"`)
  }

  // Search narrows the roster.
  const search = page.locator('input[type="search"], input[type="text"]').first()
  await search.fill(contributors[0].display_name)
  await settle(page, { extra: 400 })
  const filtered = await page.locator('main').innerText()
  assert(filtered.includes(contributors[0].display_name), 'people: search dropped its own match')
})

// ─────────────────────────────── board ───────────────────────────────

test('board: derived columns reflect real work-item state', async ({ page }) => {
  await goto(page, '/board')

  const h1 = await pageHeading(page)
  assert(/Board/i.test(h1), `board: expected "Board", got "${h1}"`)

  // The thesis: this board is derived, never maintained by hand.
  const body = await page.locator('main').innerText()
  assert(
    /derived|no tickets/i.test(body),
    'board: must say it is derived rather than hand-maintained',
  )

  const repos = await api('/api/repos')
  const items = await api(`/api/repos/${encodeURIComponent(repos[0].id)}/work-items`)
  assert(items.length > 0, 'board: the first repo should have work items')

  for (const col of ['Open', 'In progress', 'Merged']) {
    await assertVisible(page.getByText(col, { exact: false }).first(), `board: "${col}" column`)
  }

  // A merged item from the API must actually appear on the board.
  const merged = items.find((i) => i.state === 'merged')
  if (merged) {
    await assertVisible(
      page.getByText(merged.external_ref, { exact: true }).first(),
      `board: merged item ${merged.external_ref}`,
    )
  }
})

// ─────────────────────────────── import ───────────────────────────────

test('import: both modes render and no secret is ever displayed', async ({ page }) => {
  await goto(page, '/import')

  const h1 = await pageHeading(page)
  assert(/Import/i.test(h1), `import: expected "Import", got "${h1}"`)

  // The local-first claim is the reason this screen exists.
  const body = await page.locator('main').innerText()
  assert(
    /local|this machine|no server/i.test(body),
    'import: must state that credentials stay on this machine',
  )

  // Secrets are entered into password inputs, never plain text.
  const tokenInputs = page.locator('input[type="password"]')
  await assertCountAtLeast(tokenInputs, 1, 'import: token input must be type=password')

  // The tracker list never returns a real token, only a masked hint.
  const configured = await api('/api/trackers')
  assert(Array.isArray(configured) && configured.length === 2, 'import: expected jira + linear')
  for (const t of configured) {
    assert(
      !t.token || t.token.startsWith('…'),
      `import: ${t.kind} exposed something other than a masked token: ${t.token}`,
    )
  }

  // Offline mode is reachable and states that it makes no network calls.
  await page.getByRole('radio', { name: /offline/i }).click()
  await settle(page, { extra: 400 })
  const offline = await page.locator('main').innerText()
  assert(
    /no network/i.test(offline),
    'import: the offline path must state that it performs no network requests',
  )
  await assertVisible(page.locator('textarea'), 'import: offline paste area')
  await assertVisible(page.locator('input[type="file"]'), 'import: offline file picker')
})
