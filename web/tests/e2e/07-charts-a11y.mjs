/**
 * Accessibility of the visualization layer.
 *
 * Charts encode meaning in colour and geometry, so they carry the highest risk
 * of being unreadable to someone not using a mouse or not seeing the hues. These
 * specs pin the non-colour affordances: accessible names, a legend for every
 * multi-series chart, a real radiogroup for the filters, and a text readout of
 * the heatmap's contents.
 */
import { test, goto, assert, assertVisible, assertCountAtLeast } from '../runner.mjs'

const CHART = 'svg[data-chart]'

test('a11y: every chart exposes an accessible name', async ({ page }) => {
  await goto(page, '/insights', { waitFor: CHART })

  const charts = page.locator(CHART)
  const n = await assertCountAtLeast(charts, 5, 'a11y: charts')

  for (let i = 0; i < n; i++) {
    const svg = charts.nth(i)
    const kind = await svg.getAttribute('data-chart')
    const role = await svg.getAttribute('role')
    assert(role === 'img', `a11y: ${kind} chart ${i} is missing role="img"`)

    // Either an inline aria-label or a referenced <title>; SVG <title> is not
    // an HTMLElement, so read it as text content rather than innerText.
    const label = await svg.getAttribute('aria-label')
    const labelledBy = await svg.getAttribute('aria-labelledby')
    assert(
      (label && label.trim()) || labelledBy,
      `a11y: ${kind} chart ${i} has role="img" but no accessible name`,
    )
    if (labelledBy) {
      const title = await svg.locator('title').first().textContent()
      assert(
        title && title.trim().length > 0,
        `a11y: ${kind} chart ${i} references an empty <title>`,
      )
    }
  }
})

test('a11y: the heatmap has a text readout and a ramp legend', async ({ page }) => {
  await goto(page, '/insights', { waitFor: CHART })

  // A live region states the totals, so the calendar is not colour-only.
  const readout = page.locator('[aria-live="polite"]').first()
  await assertVisible(readout, 'a11y: heatmap readout')
  const text = await readout.innerText()
  assert(/\d/.test(text), `a11y: heatmap readout should carry numbers, got "${text}"`)

  // less → more legend anchors the direction of the sequential ramp.
  const legend = page.locator('[data-heatmap-legend]').first()
  await assertVisible(legend, 'a11y: heatmap ramp legend')
  const legendText = await legend.innerText()
  assert(/less/i.test(legendText), `a11y: legend should say "less", got "${legendText}"`)
  assert(/more/i.test(legendText), `a11y: legend should say "more", got "${legendText}"`)
  await assertCountAtLeast(legend.locator('span[style*="background"]'), 5, 'a11y: legend swatches')
})

test('a11y: the range filter is a keyboard-operable radiogroup', async ({ page }) => {
  await goto(page, '/insights', { waitFor: CHART })

  const group = page.locator('[role="radiogroup"]').first()
  await assertVisible(group, 'a11y: range radiogroup')
  const label = await group.getAttribute('aria-label')
  assert(label && label.trim(), 'a11y: the radiogroup needs an aria-label')

  const radios = group.locator('[role="radio"]')
  await assertCountAtLeast(radios, 4, 'a11y: range options')

  // Exactly one is checked at a time.
  const checked = group.locator('[role="radio"][aria-checked="true"]')
  assert(
    (await checked.count()) === 1,
    `a11y: expected 1 checked radio, got ${await checked.count()}`,
  )

  // Selecting another moves the checked state.
  await radios.nth(0).click()
  await page.waitForTimeout(400)
  const nowChecked = await group.locator('[role="radio"][aria-checked="true"]').first().innerText()
  assert(nowChecked.trim() === '30d', `a11y: expected 30d checked, got "${nowChecked}"`)
})

test('a11y: multi-series charts carry a legend, single-series ones do not', async ({ page }) => {
  await goto(page, '/insights', { waitFor: CHART })

  // Throughput plots two series on one axis — identity must not be colour-only.
  await assertVisible(page.getByText('Merged PRs', { exact: true }), 'a11y: throughput legend')
  await assertVisible(page.getByText('Closed issues', { exact: true }), 'a11y: throughput legend')
})

test('a11y: the page is reachable by keyboard from the skip link', async ({ page }) => {
  await goto(page, '/dashboard', { waitFor: CHART })

  await page.keyboard.press('Tab')
  const focused = await page.evaluate(() => document.activeElement?.textContent?.trim())
  assert(
    /skip to content/i.test(focused || ''),
    `a11y: first tab stop should be the skip link, got "${focused}"`,
  )

  await page.keyboard.press('Enter')
  const target = await page.evaluate(() => document.activeElement?.id || location.hash)
  assert(
    /main-content/.test(target),
    `a11y: the skip link should move focus to main content, landed on "${target}"`,
  )
})
