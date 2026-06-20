import { test, gotoApp, pageHeading, settle, url, assert, assertVisible, api } from '../runner.mjs'

// Invoices: list renders, opening an invoice shows line items, and the public
// share URL /i/:token renders the client-facing invoice with NO auth.
test('invoices: list, detail, public share', async ({ page, context }) => {
  await gotoApp(page, '/invoices')

  const h1 = await pageHeading(page)
  assert(/Invoices/i.test(h1), `invoices: h1 expected "Invoices", got "${h1}"`)

  // List: find the invoice row (seed has INV-2026-001).
  const list = await api('/api/invoices')
  const invoices = Array.isArray(list) ? list : list.invoices || []
  assert(invoices.length > 0, 'invoices: API returned no invoices (suite needs seeded data)')
  const inv = invoices[0]

  const row = page.getByRole('button').filter({ hasText: inv.number })
  await assertVisible(row, `invoices: list row for ${inv.number}`)
  await row.first().click()
  // The detail view fetches line items async — wait for them to render.
  await page.getByText('Delivered work').first().waitFor({ state: 'visible', timeout: 10000 }).catch(() => {})
  await settle(page, { extra: 200 })

  // Detail view: "Delivered work" + line items.
  await assertVisible(page.getByText('Delivered work'), 'invoices: detail "Delivered work"')
  await assertVisible(page.getByText('Total due').first(), 'invoices: detail "Total due"')

  // The public share token: read it from the API detail (authoritative), and
  // verify the detail view exposes the /i/<token> share link in the DOM.
  const detail = await api(`/api/invoices/${inv.id}`)
  const token = detail.shareToken
  assert(token, 'invoices: invoice has no shareToken (seed an issued/sent invoice)')
  await assertVisible(
    page.getByText(new RegExp(`/i/${token.slice(0, 8)}`)),
    'invoices: share URL not shown in detail view',
  )

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
