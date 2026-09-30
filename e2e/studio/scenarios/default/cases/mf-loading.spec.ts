import { test, expect } from '@playwright/test'
import { GATEWAY } from '@kb-labs/e2e-shared/urls.js'

test('ST-01: gateway serves studio app', async ({ request }) => {
  const res = await request.get(`${GATEWAY}/`)
  expect(res.status() === 404, 'Studio not served in this environment').toBe(false)
  expect(res.status()).toBe(200)
})

test('ST-02: gateway serves MF manifest JSON', async ({ request }) => {
  const res = await request.get(`${GATEWAY}/mf-manifest.json`)
  expect(res.status() === 404, 'Studio MF not enabled in this environment').toBe(false)
  expect(res.status()).toBe(200)
})

test('ST-03: all MF chunks load without 404', async ({ request }) => {
  const res = await request.get(`${GATEWAY}/studio/mf-manifest.json`)
  expect(res.status(), 'Studio MF manifest is required for chunk loading').toBe(200)
  const manifest = await res.json()
  const remotes: Record<string, { entry?: string; url?: string }> = manifest.remotes ?? manifest
  const entries = Object.values(remotes).map(remote => remote.entry ?? remote.url).filter((entry): entry is string => Boolean(entry))
  expect(entries.length, 'Studio MF manifest must contain at least one remote entry').toBeGreaterThan(0)
  for (const entry of entries) {
    const url = entry.startsWith('http') ? entry : `${GATEWAY}${entry}`
    expect((await request.get(url)).status(), `remote entry ${url} must not 404`).toBe(200)
  }
})
test('ST-04: studio app renders without JS console errors', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`${GATEWAY}/studio/`)
  await expect(page.locator('body')).not.toBeEmpty()
  expect(errors, `Studio browser errors: ${errors.join('; ')}`).toEqual([])
})
