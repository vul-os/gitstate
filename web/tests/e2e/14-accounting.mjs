import { test, gotoApp, settle, url, assert, assertVisible, api } from '../runner.mjs'

// Settings → Accounting graceful "not configured" state (no Xero/QuickBooks
// creds in dev) + the truthful invoice-detail behaviour: when no provider is
// connected the "Send to Xero/QuickBooks" push block is absent rather than
// dangling a dead Connect button.
test('accounting: not-configured state in Settings + invoice detail', async ({ page }) => {
  // Guard the precondition this whole spec is written around: dev has no creds.
  const acct = await api('/api/accounting/status')
  const providers = Array.isArray(acct) ? acct : acct.providers || []
  const anyConfigured = providers.some((p) => p.configured)
  assert(!anyConfigured, 'accounting: precondition expects no provider configured in dev')

  await gotoApp(page, '/settings')

  // The Accounting section renders.
  const heading = page.getByRole('heading', { name: 'Accounting' })
  await assertVisible(heading, 'accounting: Settings "Accounting" section heading')
  await heading.scrollIntoViewIfNeeded()
  await settle(page, { extra: 300 })

  // Graceful "not configured on this server" empty state with the setup hint
  // naming both providers' env vars — and NO Connect/Disconnect control.
  await assertVisible(
    page.getByText('No accounting provider is configured on this server.'),
    'accounting: "not configured" empty state',
  )
  await assertVisible(page.getByText('XERO_CLIENT_ID'), 'accounting: Xero setup hint env var')
  await assertVisible(page.getByText('QUICKBOOKS_CLIENT_ID'), 'accounting: QuickBooks setup hint env var')

  // No per-provider Connect button is rendered while unconfigured: the section
  // only renders the AccountingRow (with its Connect button) once a provider is
  // configured, so the empty state must carry zero Connect controls. Scope the
  // check to the Accounting SectionCard so a stray Connect elsewhere on Settings
  // (Integrations → GitHub/GitLab) doesn't trip it.
  const acctCard = page
    .locator('div')
    .filter({ has: page.getByRole('heading', { name: 'Accounting' }) })
    .filter({ hasText: 'No accounting provider is configured' })
    .last()
  const connectInCard = await acctCard.getByRole('button', { name: 'Connect' }).count()
  assert(connectInCard === 0, 'accounting: a Connect control rendered despite no provider configured')

  // Invoice detail: with nothing connected, the "Send to Xero/QuickBooks" push
  // block is absent (AccountingPush returns null when !anyConfigured). Open a
  // seeded invoice and assert the detail loads but shows no accounting push UI.
  const list = await api('/api/invoices')
  const invoices = Array.isArray(list) ? list : list.invoices || []
  assert(invoices.length > 0, 'accounting: suite needs seeded invoices')

  await page.goto(url('/invoices'), { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('main h1, h1', { state: 'visible', timeout: 20000 })
  await settle(page, { extra: 400 })
  const inv = invoices[0]
  const row = page.getByRole('button').filter({ hasText: inv.number }).first()
  await assertVisible(row, `accounting: invoice list row for ${inv.number}`)
  await row.click()

  // Detail loads (line items render) — the waitFor IS the load assertion.
  await page.getByText('Delivered work').first().waitFor({ state: 'visible', timeout: 15000 })

  // The "Send to Xero" / "Send to QuickBooks" buttons must NOT be present, and
  // the "Connect Xero or QuickBooks in Settings" prompt must NOT show either —
  // the whole Accounting push block is suppressed when nothing is configured.
  await settle(page, { extra: 300 })
  const sendBtns = await page.getByRole('button', { name: /Send to (Xero|QuickBooks)/ }).count()
  assert(sendBtns === 0, 'accounting: invoice detail showed a "Send to …" button with nothing connected')
  const pushPrompt = await page.getByText(/Connect Xero or QuickBooks in/).count()
  assert(pushPrompt === 0, 'accounting: invoice detail showed the push prompt while accounting is unconfigured')
}, { themes: false })
