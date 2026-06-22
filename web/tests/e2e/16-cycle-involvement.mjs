import { test, gotoApp, settle, pageHeading, assert, assertVisible } from '../runner.mjs'

/** Read a StatCard's big display value by its (uppercase mono) label text. */
async function statValue(page, label) {
  return page.evaluate((lbl) => {
    const spans = Array.from(document.querySelectorAll('span'))
    const labelEl = spans.find((s) => s.textContent?.trim() === lbl)
    if (!labelEl) return null
    const card = labelEl.closest('.flex.flex-col') || labelEl.parentElement?.parentElement?.parentElement
    if (!card) return null
    return card.querySelector('span.font-display')?.textContent?.trim() ?? null
  }, label)
}

// Cycle Time — the headline stat tiles render sane (non-empty, humanized) and
// the chart + raw-PR table show with the seeded merged-PR history.
test('cycle-time: stat tiles + chart + table render sane', async ({ page }) => {
  await gotoApp(page, '/cycle-time')

  const h1 = await pageHeading(page)
  assert(/Cycle Time/i.test(h1), `cycle-time: h1 expected "Cycle Time", got "${h1}"`)
  await settle(page, { extra: 500 })

  // The five lead-time tiles render real humanized values (e.g. "0.7d", "8.4h",
  // "1.2w") — not the "—" empty placeholder.
  const HUMANIZED = /^\d+(\.\d+)?(m|h|d|w)$/
  for (const label of ['Avg', 'Median (p50)', 'p90', 'Fastest', 'Slowest']) {
    const v = await statValue(page, label)
    assert(v && v !== '—', `cycle-time: "${label}" tile empty ("${v}") — seeded PRs should populate it`)
    assert(HUMANIZED.test(v), `cycle-time: "${label}" tile not a humanized duration ("${v}")`)
  }

  // Chart card renders with a non-empty plotted SVG (not the empty-state text).
  await assertVisible(
    page.getByRole('heading', { name: 'Lead time per merged PR' }),
    'cycle-time: chart card heading',
  )
  const empty = await page.getByText(/No cycle-time data in this range/).count()
  assert(empty === 0, 'cycle-time: chart stuck on empty state with seeded data')
  const paths = await page.locator('svg path').count()
  assert(paths > 0, 'cycle-time: chart SVG has no plotted paths')

  // Raw PR table renders rows (only shown when there is data).
  await assertVisible(
    page.getByRole('heading', { name: 'Merged pull requests' }),
    'cycle-time: PR table heading',
  )
  const rows = await page.locator('table tbody tr').count()
  assert(rows > 0, 'cycle-time: merged-PR table has no rows')
}, { themes: false })

// Involvement — period filter REGRESSION GUARD. The recently-fixed bug made the
// period switch a no-op and inflated per-card counts. This asserts: (1) the card
// set re-renders when switching 7d → 90d (a per-card review count actually
// changes), and (2) per-user counts stay the small per-person numbers (a sane
// upper bound), NOT the ~108-ish cohort-inflated figure the regression produced.
test('involvement: period filter re-renders cards with sane per-user counts', async ({ page }) => {
  await gotoApp(page, '/involvement')

  const h1 = await pageHeading(page)
  assert(/Involvement/i.test(h1), `involvement: h1 expected "Involvement", got "${h1}"`)
  await settle(page, { extra: 500 })

  // Default 30d: cards render and the member tally is the small per-org roster
  // (seed = 12), NOT an inflated number.
  const memberTally = page.getByText(/\d+ members?/)
  await assertVisible(memberTally, 'involvement: member tally chip')
  const tallyText = (await memberTally.first().innerText()).trim()
  const memberCount = Number(tallyText.replace(/[^\d]/g, ''))
  assert(memberCount > 0 && memberCount < 30, `involvement: member count not a sane roster size ("${tallyText}")`)

  // The "Reviews done" COHORT total at 30d (this is a legitimate sum across the
  // team, so it can be large) — capture it to prove the period switch changes it.
  const reviews30 = await statValue(page, 'Reviews done')
  const reviews30Num = Number((reviews30 || '').replace(/[^\d]/g, ''))
  assert(reviews30Num > 0, `involvement: 30d "Reviews done" total empty ("${reviews30}")`)

  // Read the per-card review counts at 30d. Each card's review value is the
  // numeric span at the end of the "Reviews done" DimBar — the per-USER figure,
  // which must stay small (the regression inflated these toward the cohort sum).
  async function perUserReviewCounts() {
    return page.evaluate(() => {
      // Each InvolvementCard has DimBars; the "Reviews done" bar's label span
      // contains that text and the value is the trailing numeric span sibling.
      const labels = Array.from(document.querySelectorAll('span')).filter(
        (s) => s.textContent?.trim() === 'Reviews done' && s.classList.contains('font-mono'),
      )
      const out = []
      for (const lbl of labels) {
        const row = lbl.parentElement // the DimBar flex row
        if (!row) continue
        const nums = Array.from(row.querySelectorAll('span.tabular-nums'))
        const v = nums.length ? Number(nums[nums.length - 1].textContent.replace(/[^\d]/g, '')) : NaN
        if (!Number.isNaN(v)) out.push(v)
      }
      return out
    })
  }
  const perUser30 = await perUserReviewCounts()
  assert(perUser30.length > 0, 'involvement: no per-card "Reviews done" values found')
  // Per-user reviews over 30d are bounded by a sane ceiling — the regression
  // pushed a single card toward the cohort sum (~108). Guard well below that.
  const maxPerUser30 = Math.max(...perUser30)
  assert(
    maxPerUser30 < reviews30Num,
    `involvement: a per-user review count (${maxPerUser30}) reached the cohort total (${reviews30Num}) — regression`,
  )
  assert(maxPerUser30 < 100, `involvement: per-user 30d reviews implausibly high (${maxPerUser30}) — regression guard`)

  // Switch period 30d → 7d: the period filter must actually take effect. The
  // cohort "Reviews done" total must CHANGE (the seed has fewer reviews in 7d).
  await page.getByRole('button', { name: '7 days' }).click()
  await settle(page, { extra: 600 })
  // Wait until the total differs from the 30d value (the re-fetch landed).
  await page.waitForFunction(
    (prev) => {
      const spans = Array.from(document.querySelectorAll('span'))
      const lbl = spans.find((s) => s.textContent?.trim() === 'Reviews done')
      const card = lbl?.closest('.flex.flex-col')
      const val = card?.querySelector('span.font-display')?.textContent?.trim()
      return val && val !== prev
    },
    reviews30,
    { timeout: 12000 },
  ).catch(() => {})

  const reviews7 = await statValue(page, 'Reviews done')
  assert(
    reviews7 !== reviews30,
    `involvement: period switch was a no-op — "Reviews done" stayed "${reviews30}" for both 7d and 30d (REGRESSION)`,
  )
  const reviews7Num = Number((reviews7 || '').replace(/[^\d]/g, ''))
  // 7d should be a strict subset of 30d activity → fewer reviews.
  assert(reviews7Num < reviews30Num, `involvement: 7d reviews (${reviews7Num}) not < 30d reviews (${reviews30Num})`)

  // And the roster size stays the same small order of magnitude across periods.
  const tally7 = (await page.getByText(/\d+ members?/).first().innerText()).trim()
  const memberCount7 = Number(tally7.replace(/[^\d]/g, ''))
  assert(memberCount7 > 0 && memberCount7 < 30, `involvement: 7d member count not sane ("${tally7}")`)
}, { themes: false })
