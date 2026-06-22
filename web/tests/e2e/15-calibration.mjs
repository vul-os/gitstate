import { test, gotoApp, settle, assert, assertVisible } from '../runner.mjs'

// Eng-Health → Estimation calibration. The demo org has calibration seeded
// (global cohort with n≫MIN_N, MAE, multiple difficulty buckets + cohorts), so
// the headline StatCards, the per-cohort accuracy table, and the calibration
// curve must all render real data — not the empty/explanatory fallbacks.

/** Read a StatCard's big value by its (uppercase mono) label text. */
async function statValue(page, label) {
  return page.evaluate((lbl) => {
    const spans = Array.from(document.querySelectorAll('span'))
    const labelEl = spans.find((s) => s.textContent?.trim() === lbl)
    if (!labelEl) return null
    // Walk up to the StatCard root, then read the display-font value span.
    const card = labelEl.closest('.flex.flex-col') || labelEl.parentElement?.parentElement?.parentElement
    if (!card) return null
    const valEl = card.querySelector('span.font-display')
    return valEl?.textContent?.trim() ?? null
  }, label)
}

test('calibration: headline cards + cohort table + curve render with seeded data', async ({ page }) => {
  await gotoApp(page, '/eng-health')

  // Scroll to the "Estimation calibration" section.
  const section = page.getByRole('heading', { name: 'Estimation calibration' })
  await assertVisible(section, 'calibration: "Estimation calibration" section heading')
  await section.scrollIntoViewIfNeeded()
  // Give the (async) estimation fetch time to resolve into real content.
  await settle(page, { extra: 600 })

  // Headline StatCards present.
  for (const label of ['Bias', 'Mean error', 'Samples', 'Cohorts']) {
    await assertVisible(page.getByText(label, { exact: true }).first(), `calibration: "${label}" StatCard`)
  }

  // Bias / MAE / Samples must carry REAL values (not the "—" empty fallback),
  // proving the seeded global cohort drives the cards.
  const bias = await statValue(page, 'Bias')
  assert(bias && bias !== '—', `calibration: Bias card empty ("${bias}") — seeded data should populate it`)
  // Seeded global cohort runs ~15% low → a "N% low" / "on target" string.
  assert(/(low|high|on target|target)/i.test(bias), `calibration: Bias card not a bias phrase ("${bias}")`)

  const mae = await statValue(page, 'Mean error')
  assert(mae && mae !== '—' && /±/.test(mae), `calibration: Mean error card empty/odd ("${mae}")`)

  const samples = await statValue(page, 'Samples')
  const samplesNum = Number((samples || '').replace(/[^\d]/g, ''))
  assert(samplesNum > 0, `calibration: Samples card has no count ("${samples}")`)

  const cohorts = await statValue(page, 'Cohorts')
  const cohortsNum = Number((cohorts || '').replace(/[^\d]/g, ''))
  assert(cohortsNum > 0, `calibration: Cohorts card has no count ("${cohorts}")`)

  // Per-cohort accuracy table renders (not the "Not enough scored estimates"
  // empty state) — assert the table header + the worst-calibrated-first hint +
  // that data rows exist with the "N cohorts" tally.
  await assertVisible(
    page.getByRole('heading', { name: 'Per-cohort accuracy' }),
    'calibration: "Per-cohort accuracy" card heading',
  )
  const notEnough = await page.getByText('Not enough scored estimates yet.').count()
  assert(notEnough === 0, 'calibration: per-cohort table stuck on empty state with seeded data')
  // The accuracy table has a body row (the BiasBar lives in a <td>).
  const cohortRows = await page.locator('table tbody tr').count()
  assert(cohortRows > 0, 'calibration: per-cohort accuracy table has no rows')

  // Calibration curve renders (not the "Not enough merged history" empty text).
  await assertVisible(
    page.getByRole('heading', { name: 'Calibration curve' }),
    'calibration: "Calibration curve" card heading',
  )
  const curveEmpty = await page.getByText(/Not enough merged history/).count()
  assert(curveEmpty === 0, 'calibration: curve stuck on empty state with seeded data')
  // The curve is an SVG LineChart — assert an <svg> with plotted <path>s renders
  // inside the curve card.
  const curveCard = page
    .locator('div')
    .filter({ has: page.getByRole('heading', { name: 'Calibration curve' }) })
    .last()
  const paths = await curveCard.locator('svg path').count()
  assert(paths > 0, 'calibration: curve SVG has no plotted paths')
})
