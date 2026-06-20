import { test, gotoApp, pageHeading, settle, assert, assertVisible, api, apiPatch } from '../runner.mjs'

/**
 * Resolve the droppable card-list element for a column by its header label.
 *
 * The column wrapper (`div.flex.flex-col.w-[276px]`) contains the uppercase
 * header span *and*, as a sibling, the actual dnd-kit droppable (the scrollable
 * card list with `ref={setNodeRef}` — the rounded `overflow-y-auto flex-1` div).
 * dnd-kit uses `closestCorners` collision detection, so we want to drop over the
 * center of that list body, not the header.
 */
function columnBody(page, label) {
  const wrapper = page
    .locator('div.flex.flex-col.w-\\[276px\\]')
    .filter({ has: page.locator('span.uppercase.tracking-widest', { hasText: label }) })
    .first()
  // The droppable is the rounded, scrollable list div within the wrapper.
  return wrapper.locator('div.overflow-y-auto.rounded-xl, div.rounded-xl.overflow-y-auto').first()
}

/**
 * Perform a single dnd-kit pointer drag from `cardLocator` to the center of
 * `targetLocator`. dnd-kit's PointerSensor needs:
 *   - pointer-down on the card,
 *   - an initial move that crosses the 8px activation distance,
 *   - several intermediate moves so `dragOver` fires over the destination
 *     droppable (collisionDetection runs on pointer move),
 *   - pointer-up while the cursor is over the destination.
 * Re-reads bounding boxes each call so a retry uses fresh positions.
 */
async function performDrag(page, cardLocator, targetLocator) {
  const card = await cardLocator.boundingBox()
  const target = await targetLocator.boundingBox()
  assert(card, 'board: source card has no bounding box')
  assert(target, 'board: target column body has no bounding box')

  const startX = card.x + card.width / 2
  const startY = card.y + card.height / 2
  // Aim for the center of the destination column body (clamped to a visible band
  // so a tall/short column still gets a point well inside the droppable).
  const endX = target.x + target.width / 2
  const endY = target.y + Math.max(40, Math.min(target.height / 2, target.height - 40))

  await page.mouse.move(startX, startY)
  await page.mouse.down()
  // Cross the 8px activation threshold deliberately, with a tiny settle so the
  // sensor latches the drag before we glide away.
  await page.mouse.move(startX + 14, startY + 6, { steps: 4 })
  await page.waitForTimeout(40)
  // Glide to the target in several discrete hops, each itself stepped, so a
  // continuous stream of dragOver events fires over the intervening columns and
  // finally settles over the destination.
  const hops = 14
  for (let i = 1; i <= hops; i++) {
    await page.mouse.move(
      startX + ((endX - startX) * i) / hops,
      startY + ((endY - startY) * i) / hops,
      { steps: 2 },
    )
    if (i % 4 === 0) await page.waitForTimeout(20)
  }
  // Park squarely on the destination and let dragOver register before release.
  await page.mouse.move(endX, endY, { steps: 4 })
  await page.waitForTimeout(140)
  await page.mouse.up()
}

/** Poll the API for the issue's effective state, capped tight (≤ capMs). */
async function waitForState(issueId, wantState, { capMs = 8_000, stepMs = 350 } = {}) {
  const deadline = Date.now() + capMs
  for (;;) {
    const after = await api('/api/issues')
    const list = Array.isArray(after) ? after : after.issues || []
    const found = list.find((i) => i.id === issueId)
    const eff = found?.manualStateOverride ?? found?.derivedState ?? found?.state
    if (eff === wantState) return true
    if (Date.now() >= deadline) return false
    await new Promise((r) => setTimeout(r, stepMs))
  }
}

// Columns render, and dragging a card to another column persists via the API.
test('board: columns render + drag persists', async ({ page }) => {
  await gotoApp(page, '/board', { waitFor: 'h1' })

  // Page heading (board page titles the work view "Work").
  const h1 = await pageHeading(page)
  assert(/Work/i.test(h1), `board: h1 expected "Work", got "${h1}"`)

  // All four kanban columns render. Column headers are uppercase spans with
  // tracking-widest; scope to those to avoid matching the (hidden) state-filter
  // <option> elements that share the same label text.
  for (const label of ['Open', 'In Progress', 'Done', 'Closed']) {
    const header = page.locator('span.uppercase.tracking-widest', { hasText: label })
    await assertVisible(header.first(), `board: column "${label}"`)
  }

  // Pick a known clean "open" native issue from the API (no derivedState/override),
  // so the move is observable and reversible.
  const issues = await api('/api/issues')
  const list = Array.isArray(issues) ? issues : issues.issues || []
  const candidate = list.find(
    (i) =>
      i.manualStateOverride == null &&
      i.derivedState == null &&
      i.state === 'open' &&
      i.source === 'native' &&
      typeof i.title === 'string',
  )
  assert(candidate, 'board: no clean open native issue found to drag')

  const targetBody = columnBody(page, 'In Progress')
  await assertVisible(targetBody, 'board: "In Progress" column drop area')

  // Re-locate the card fresh each attempt (the DOM re-renders after a move).
  const cardFor = () => page.locator('div').filter({ hasText: candidate.title }).last()
  await assertVisible(cardFor(), `board: card "${candidate.title}" on board`)

  // Drag, then poll the API with a tight cap. If a headless drag silently misses,
  // retry the whole gesture once (fresh positions) so a flaky drag self-heals
  // while a genuinely broken drag still fails fast (≈16s worst case, not 60s).
  let moved = false
  for (let attempt = 1; attempt <= 2 && !moved; attempt++) {
    const card = cardFor()
    await assertVisible(card, `board: card "${candidate.title}" before drag attempt ${attempt}`)
    await performDrag(page, card, targetBody)
    await settle(page, { extra: 250 })
    moved = await waitForState(candidate.id, 'in_progress', { capMs: 8_000 })
  }

  // Restore original state regardless of assertion outcome (idempotent re-runs).
  try {
    await apiPatch(`/api/issues/${candidate.id}`, { state: 'open' })
  } catch {
    /* best-effort restore */
  }

  assert(
    moved,
    `board: card "${candidate.title}" did not persist to in_progress after 2 drag attempts`,
  )
})
