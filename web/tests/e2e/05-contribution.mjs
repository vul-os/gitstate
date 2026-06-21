import { test, gotoApp, pageHeading, settle, assert, assertVisible } from '../runner.mjs'

/** Read the ordered list of contributor names from the roster cards. */
async function rosterNames(page) {
  // Each ContributorCard renders the name in a semibold <p>. Scope to the
  // People section's card buttons (role=button) to keep order stable.
  const names = await page.evaluate(() => {
    const cards = Array.from(document.querySelectorAll('[role="button"]'))
      .filter((el) => el.querySelector('p.text-sm.font-semibold'))
    return cards.map((el) => el.querySelector('p.text-sm.font-semibold')?.textContent?.trim() || '')
  })
  return names.filter(Boolean)
}

// Roster loads, tabs switch, a weight slider re-orders the ranking, and the
// contributor drawer opens with evidence.
test('contribution: roster, tabs, slider re-rank, drawer', async ({ page }) => {
  await gotoApp(page, '/contribution')

  const h1 = await pageHeading(page)
  assert(/Contribution/i.test(h1), `contribution: h1 expected "Contribution", got "${h1}"`)

  // Default People tab content (caveat banner is a stable People-tab anchor).
  await assertVisible(
    page.getByText('Advisory, multi-dimensional, evidence-backed.'),
    'contribution: People-tab caveat banner',
  )

  // Roster has multiple contributors.
  const before = await rosterNames(page)
  assert(before.length >= 2, `contribution: expected >=2 contributors, got ${before.length}`)

  // Tab switching: Over time renders without errors.
  await page.getByRole('button', { name: 'Over time' }).click()
  await settle(page, { extra: 300 })
  await assertVisible(
    page.getByText('Composite over time'),
    'contribution: "Composite over time" after switching to Over time tab',
  )
  // Back to People for the re-rank test.
  await page.getByRole('button', { name: 'People', exact: true }).click()
  await settle(page, { extra: 300 })

  // Drag a weight slider to an extreme and assert the ranking reorders.
  // Set Durability to max and the rest to 0 — a strong, deterministic re-sort.
  const sliders = {
    Shipped: page.getByLabel('Shipped weight'),
    Review: page.getByLabel('Review weight'),
    Effort: page.getByLabel('Effort weight'),
    Quality: page.getByLabel('Quality weight'),
    Ownership: page.getByLabel('Ownership weight'),
    Durability: page.getByLabel('Durability weight'),
  }
  await assertVisible(sliders.Review, 'contribution: Review weight slider')

  // Move the sliders with real keyboard input so React's controlled onChange
  // fires (range inputs ignore fill()). Home => min(0), End => max(10).
  // We weight purely by "Review" — a dimension that discriminates the seeded
  // roster and produces a clearly different order than the default ranking
  // (the default is shipped-dominant; review pushes reviewers to the top).
  const dominant = 'Review'
  async function setSliderMin(locator) {
    await locator.focus()
    await locator.press('Home')
  }
  async function setSliderMax(locator) {
    await locator.focus()
    await locator.press('End')
  }
  for (const [name, loc] of Object.entries(sliders)) {
    if (name === dominant) await setSliderMax(loc)
    else await setSliderMin(loc)
  }
  await settle(page, { extra: 400 })

  // Sanity: the dominant slider really moved to its max (10).
  const domVal = await sliders[dominant].inputValue()
  assert(domVal === '10', `contribution: ${dominant} slider expected 10, got ${domVal}`)

  const after = await rosterNames(page)
  assert(after.length === before.length, 'contribution: roster size changed after slider move')
  // The order must differ (a single-dimension weighting reorders the seeded roster).
  const reordered = before.join('|') !== after.join('|')
  assert(
    reordered,
    `contribution: ranking did not reorder after slider change.\n      before: ${before.join(', ')}\n      after:  ${after.join(', ')}`,
  )

  // Open a contributor drawer and assert evidence shows.
  const firstCard = page.locator('[role="button"]').filter({ has: page.locator('p.text-sm.font-semibold') }).first()
  await firstCard.click()
  const drawer = page.getByRole('dialog', { name: 'Contributor evidence' })
  await assertVisible(drawer, 'contribution: contributor evidence drawer')
  // Evidence dimension blocks render (e.g. dimension labels like "Shipped").
  // Wait for the async evidence fetch + render rather than a fixed delay.
  await drawer.getByText('Shipped', { exact: true }).first()
    .waitFor({ state: 'visible', timeout: 8000 })
  await assertVisible(
    drawer.getByText('Shipped', { exact: true }).first(),
    'contribution: drawer "Shipped" dimension block',
  )
  const composite = await drawer.getByText('/ 100 composite').count()
  assert(composite > 0, 'contribution: drawer missing composite score')
}, { themes: false })
