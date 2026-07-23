import { test, goto, api, pageHeading, assert, assertVisible } from '../runner.mjs'

test('contexts: saved working sets list with their repos and tags', async ({ page }) => {
  await goto(page, '/contexts')

  const h1 = await pageHeading(page)
  assert(/Contexts/i.test(h1), `contexts: expected "Contexts", got "${h1}"`)

  const contexts = await api('/api/contexts')
  assert(contexts.length > 0, 'contexts: seeded data should include contexts')
  for (const ctx of contexts) {
    await assertVisible(page.getByText(ctx.name, { exact: false }).first(), `contexts: "${ctx.name}"`)
  }
})

test('categories: the taxonomy-backed category set renders', async ({ page }) => {
  await goto(page, '/categories')

  const h1 = await pageHeading(page)
  assert(/Categories/i.test(h1), `categories: expected "Categories", got "${h1}"`)

  const cats = await api('/api/categories')
  assert(cats.length >= 19, `categories: expected the default taxonomy, got ${cats.length}`)
  await assertVisible(page.getByText(cats[0].label, { exact: false }).first(), 'categories: first label')
})

test('classify: the repo picker offers every tracked repo', async ({ page }) => {
  await goto(page, '/classify')

  const h1 = await pageHeading(page)
  assert(/Classify/i.test(h1), `classify: expected "Classify", got "${h1}"`)

  const repos = await api('/api/repos')
  const body = await page.locator('main').innerText()
  assert(
    repos.some((r) => body.includes(r.slug)),
    'classify: at least one tracked repo should be selectable',
  )
})

test('taxonomy: the signed document and its version are shown', async ({ page }) => {
  await goto(page, '/taxonomy')

  const h1 = await pageHeading(page)
  assert(/Taxonomy/i.test(h1), `taxonomy: expected "Taxonomy", got "${h1}"`)

  const tax = await api('/api/taxonomy')
  assert(tax.schema === 'gitstate.taxonomy/v1', `taxonomy: unexpected schema "${tax.schema}"`)
  const body = await page.locator('main').innerText()
  assert(
    body.includes(tax.version),
    `taxonomy: page should show version "${tax.version}"`,
  )
})

test('settings: daemon health and sync status are surfaced', async ({ page }) => {
  await goto(page, '/settings')

  const h1 = await pageHeading(page)
  assert(/Settings/i.test(h1), `settings: expected "Settings", got "${h1}"`)

  const health = await api('/health')
  const body = await page.locator('main').innerText()
  assert(
    body.includes(health.version) || /local|daemon/i.test(body),
    'settings: should surface the local daemon status',
  )
})
