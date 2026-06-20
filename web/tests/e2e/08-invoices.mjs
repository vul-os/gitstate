import { test, gotoApp, pageHeading, settle, url, assert, assertVisible, api } from '../runner.mjs'

// Invoices: list renders, opening an invoice shows line items, and the public
// share URL /i/:token renders the client-facing invoice with NO auth.
test('invoices: list, detail, public share', async ({ page, context }) => {
  await gotoApp(page, '/invoices')

  const h1 = await pageHeading(page)
  assert(/Invoices/i.test(h1), `invoices: h1 expected "Invoices", got "${h1}"`)

  const list = await api('/api/invoices')
  const invoices = Array.isArray(list) ? list : list.invoices || []
  assert(invoices.length > 0, 'invoices: API returned no invoices (suite needs seeded data)')

  // Use an invoice that has a public share token (a 'sent' one in the seed) so the
  // single detail view we open covers BOTH the line-items check AND the share link.
  let token = null
  let inv = invoices[0]
  for (const x of invoices) {
    const d = await api(`/api/invoices/${x.id}`)
    if (d.shareToken) { token = d.shareToken; inv = x; break }
  }
  assert(token, 'invoices: no invoice has a shareToken (seed an issued/sent invoice)')

  // Wait for the list to fully render (rows wired) before clicking — clicking too
  // early races React's onClick attach and the detail never opens.
  await settle(page, { extra: 400 })
  const row = page.getByRole('button').filter({ hasText: inv.number }).first()
  await assertVisible(row, `invoices: list row for ${inv.number}`)
  await row.click()

  // Detail view loads line items async — the waitFor IS the assertion.
  await page.getByText('Delivered work').first().waitFor({ state: 'visible', timeout: 15000 })
  await assertVisible(page.getByText('Total due').first(), 'invoices: detail "Total due"')
  // The detail exposes the /i/<token> public share link.
  await page.getByText(new RegExp(`/i/${token.slice(0, 8)}`)).first()
    .waitFor({ state: 'visible', timeout: 10000 })

  // Visit the public share URL in a brand-new context with NO auth.
  const anonCtx = await context.browser().newContext({
    viewport: { width: 1280, height: 900 },
  })
  try {
    const anon = await anonCtx.newPage()
    // Sanity: no token in this context.
    await anon.goto(url(`/i/${token}`), { waitUntil: 'domcontentloaded' })
    const anonTok = await anon.evaluate(() => localStorage.getItem('gs_access_token'))
    assert(!anonTok, 'invoices: anon share context unexpectedly has an auth token')
    await anon.waitForSelector('h1', { timeout: 20_000 })

    // Client-facing invoice number is the only h1.
    const shareH1 = (await anon.locator('h1').first().innerText()).trim()
    assert(
      shareH1.includes(inv.number),
      `invoices: public share h1 expected to contain "${inv.number}", got "${shareH1}"`,
    )
    // Line items + trust banner render for the client.
    await assertVisible(anon.getByText('Delivered work'), 'invoices: public "Delivered work"')
    await assertVisible(
      anon.getByText('Every line is backed by merged pull requests.'),
      'invoices: public trust banner',
    )
    await assertVisible(anon.getByText('Total due').first(), 'invoices: public "Total due"')
  } finally {
    await anonCtx.close()
  }
}, { themes: false })
