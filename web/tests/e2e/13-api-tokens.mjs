import { test, gotoApp, settle, assert, assertVisible } from '../runner.mjs'

// Settings → API tokens (owner-gated; demo is owner of "Acme Dev Shop").
// Full lifecycle: create with a scope selection → the raw gsk_… secret shows
// exactly ONCE with a copy control → the token appears in the list by name →
// revoke it → it's marked revoked. Each theme run mints a uniquely-named token
// so the two theme passes (and reruns) never collide.
test('api tokens: create → reveal once → list → revoke', async ({ page, theme }) => {
  await gotoApp(page, '/settings')

  // A unique name per theme + run so concurrent/sequential passes don't clash.
  const name = `pw-tok-${theme}-${Date.now().toString(36)}`

  // Scroll the "API tokens" section into view and open the create form.
  const sectionHeading = page.getByRole('heading', { name: 'API tokens' })
  await assertVisible(sectionHeading, 'api-tokens: section heading')
  await sectionHeading.scrollIntoViewIfNeeded()

  const newBtn = page.getByRole('button', { name: 'New token' })
  await assertVisible(newBtn, 'api-tokens: "New token" button (owner-gated, demo is owner)')
  await newBtn.click()

  // Form opens. Fill the name and add a write scope on top of the defaults
  // (read:issues + read:context are pre-selected) so we exercise scope selection.
  await page.getByPlaceholder('e.g. CI agent, local gittrack').fill(name)
  // The scope toggle is a <button aria-pressed> whose accessible name includes
  // both the scope key and its description, so match by the inner mono label.
  const scopeBtn = page.locator('button[aria-pressed]').filter({ hasText: 'write:agent_runs' }).first()
  await assertVisible(scopeBtn, 'api-tokens: write:agent_runs scope toggle')
  await scopeBtn.click()
  // aria-pressed flips to true once selected.
  await assert(
    (await scopeBtn.getAttribute('aria-pressed')) === 'true',
    'api-tokens: write:agent_runs scope did not toggle on',
  )

  await page.getByRole('button', { name: 'Create token' }).click()

  // The raw gsk_… secret is revealed exactly once, with the one-time warning,
  // a Copy control, and the export hint. Wait for the reveal panel, then scope
  // the secret read to the panel (the token LIST also renders a `gsk_…` prefix
  // <code>, so a page-wide `code` match would grab the short prefix instead).
  await page.getByText("Copy your token now — you won't see it again")
    .waitFor({ state: 'visible', timeout: 15000 })
  const revealPanel = page
    .locator('div')
    .filter({ has: page.getByText("Copy your token now — you won't see it again") })
    .filter({ has: page.locator('code') })
    .last()
  const secretCode = revealPanel.locator('code').filter({ hasText: /^gsk_/ }).first()
  await assertVisible(secretCode, 'api-tokens: revealed gsk_… secret')
  const secret = (await secretCode.innerText()).trim()
  assert(/^gsk_/.test(secret), `api-tokens: revealed secret should start with gsk_, got "${secret.slice(0, 12)}…"`)
  // The full secret is much longer than the 8-char list prefix — sanity-check we
  // captured the real one-time token, not a truncated prefix.
  assert(secret.length > 16, `api-tokens: revealed secret looks truncated ("${secret}")`)

  // A copy control accompanies the secret (the reveal panel's "Copy" button).
  await assertVisible(
    page.getByRole('button', { name: 'Copy' }).first(),
    'api-tokens: copy control on the revealed secret',
  )
  // The CLI usage hint embeds the same token, confirming it's shown only here.
  await assertVisible(
    page.getByText(/export GITSTATE_TOKEN=gsk_/),
    'api-tokens: export GITSTATE_TOKEN hint',
  )

  // Dismiss the one-time reveal and wait for the reveal panel to unmount.
  await page.getByRole('button', { name: "Done — I've stored it" }).click()
  await page.getByText("Copy your token now — you won't see it again")
    .waitFor({ state: 'detached', timeout: 10000 })

  // The new token now appears in the list by name (the secret is NOT shown again).
  await page.getByText(name, { exact: true }).first().waitFor({ state: 'visible', timeout: 10000 })
  await assertVisible(page.getByText(name, { exact: true }).first(), `api-tokens: list row for "${name}"`)

  // The raw secret must not be visible anywhere on the page after dismissal.
  const stillShown = await page.locator('code').filter({ hasText: secret }).count()
  assert(stillShown === 0, 'api-tokens: full secret still visible after dismiss (should show only once)')

  // Revoke it: scope the Revoke/Confirm clicks to the row for THIS token so we
  // never touch a seeded token. The TokenRow is the nearest ancestor that holds
  // both the name and the Revoke button.
  const tokenRow = page
    .locator('div.py-3\\.5')
    .filter({ has: page.getByText(name, { exact: true }) })
    .first()
  await assertVisible(tokenRow, `api-tokens: token row container for "${name}"`)
  await tokenRow.getByRole('button', { name: 'Revoke' }).click()
  await tokenRow.getByRole('button', { name: 'Confirm' }).click()

  // After revoke the row is marked "revoked" (kept, struck-through + badge).
  await tokenRow.getByText('revoked').waitFor({ state: 'visible', timeout: 10000 })
  await assertVisible(tokenRow.getByText('revoked'), `api-tokens: "${name}" marked revoked`)
  // The Revoke action is gone for a revoked token.
  await settle(page, { extra: 200 })
  const revokeStill = await tokenRow.getByRole('button', { name: 'Revoke' }).count()
  assert(revokeStill === 0, 'api-tokens: Revoke button still present on a revoked token')
})
