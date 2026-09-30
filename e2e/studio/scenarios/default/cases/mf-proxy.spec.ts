import { test, expect } from '@playwright/test'
import { GATEWAY } from '@kb-labs/e2e-shared/urls.js'

// Regression suite for Studio MF proxy issues — gateway must serve all MF assets
// correctly so Module Federation bootstrap works in the browser.

test('MF-01: MF manifest is served with correct Content-Type', async ({ request }) => {
  const res = await request.get(`${GATEWAY}/studio/mf-manifest.json`)
  expect(res.status() === 404, 'Studio not deployed in this environment').toBe(false)
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('application/json')
})

test('MF-02: remoteEntry.js served as JavaScript (not HTML/text)', async ({ request }) => {
  // If gateway returns HTML (404 page) for JS chunks the MF bootstrap silently fails
  const manifest = await request.get(`${GATEWAY}/studio/mf-manifest.json`)
  expect(manifest.status() === 404, 'Studio not deployed in this environment').toBe(false)

  const body = await manifest.json()
  // Find first remote entry URL from manifest
  const remotes: Record<string, { entry?: string; url?: string }> = body.remotes ?? body
  const entryUrl = Object.values(remotes)[0]?.entry ?? Object.values(remotes)[0]?.url
  expect(!entryUrl, 'No remoteEntry URL in manifest').toBe(false)

  const res = await request.get(entryUrl!.startsWith('http') ? entryUrl! : `${GATEWAY}${entryUrl}`)
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('javascript')
})

test('MF-03: MF assets have CORS headers (required for cross-origin MF loading)', async ({ request }) => {
  const manifest = await request.get(`${GATEWAY}/studio/mf-manifest.json`)
  expect(manifest.status() === 404, 'Studio not deployed in this environment').toBe(false)
  // CORS must be present so browser can load MF chunks from gateway origin
  expect(
    manifest.headers()['access-control-allow-origin'] ?? manifest.headers()['vary'],
  ).toBeTruthy()
})

test('MF-04: JS chunks return 200 not redirect (proxy rewrite must not double-prefix)', async ({ request }) => {
  // Regression: proxy was rewriting /studio/assets/ → /studio/studio/assets/ (double prefix)
  const res = await request.get(`${GATEWAY}/studio/assets/`)
  // 404 is fine (dir listing off), but must not be 301/302/307 (redirect loop)
  expect([200, 403, 404]).toContain(res.status())
  expect([301, 302, 307, 308]).not.toContain(res.status())
})

test('MF-05: unknown studio asset returns 404 not 500', async ({ request }) => {
  const res = await request.get(`${GATEWAY}/studio/assets/nonexistent-chunk-e2e-test.js`)
  expect([404, 403]).toContain(res.status())
})

test('MF-06: all remoteEntry URLs in manifest resolve to 200', async ({ request }) => {
  const manifest = await request.get(`${GATEWAY}/studio/mf-manifest.json`)
  expect(manifest.status(), 'Studio MF manifest is required for remote-entry checks').toBe(200)
  const body = await manifest.json()
  const remotes: Record<string, { entry?: string; url?: string }> = body.remotes ?? body
  const entries = Object.values(remotes).map(remote => remote.entry ?? remote.url).filter((entry): entry is string => Boolean(entry))
  expect(entries.length, 'Studio MF manifest must declare remote entries').toBeGreaterThan(0)
  for (const entry of entries) {
    const url = entry.startsWith('http') ? entry : `${GATEWAY}${entry}`
    const response = await request.get(url)
    expect(response.status(), `remote entry ${url} must load`).toBe(200)
    expect(response.headers()['content-type'], `remote entry ${url} must be JavaScript`).toContain('javascript')
  }
})

test('MF-07: studio app boots in browser without JS errors', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`${GATEWAY}/studio/`)
  await expect(page.locator('body')).not.toBeEmpty()
  expect(errors, `Studio browser errors: ${errors.join('; ')}`).toEqual([])
})
