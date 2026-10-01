import { test, expect } from '@playwright/test'
import { mkdtemp, mkdir, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const GATEWAY = (process.env.GATEWAY_URL ?? 'http://127.0.0.1:4000').replace(/\/+$/, '')
const enabled = process.env.KB_STUDIO_FIRST_TIER_JOURNEY === '1'

test('S-120.1 and S-020.6: dashboard frame and first-tier navigation', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')

  await page.goto(`${GATEWAY}/`)
  await expect(page.getByText('Intelligence Dashboard', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible()
  await expect(page.getByText('Gateway health', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Projects' })).toBeVisible()

  for (const [label, path, title] of [
    ['Projects', '/projects', 'Projects'],
    ['Analytics', '/analytics/overview', 'Analytics Overview'],
    ['Observability', '/observability/logs', 'Logs'],
  ]) {
    await page.getByRole('link', { name: label, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`${path.replaceAll('/', '\\/')}$`))
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  }
})

test('S-046.4-.6 and S-124.7: inspect, register, and safely unregister a project', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  const projectPath = await mkdtemp(join(tmpdir(), 'studio-project-case-'))
  const projectName = projectPath.split('/').at(-1)!

  try {
    await page.goto(`${GATEWAY}/projects`)
    await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
    await expect(page.getByText('Name', { exact: true })).toBeVisible()
    await expect(page.getByText('Status', { exact: true })).toBeVisible()
    await expect(page.getByText('.kb/', { exact: true })).toBeVisible()
    await expect(page.getByText('Last used', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: 'Add project', exact: true }).click()
    await page.getByLabel('Folder path').fill(projectPath)
    await page.getByRole('button', { name: 'Register', exact: true }).click()
    await expect(page.getByText('Initialize project?', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Back', exact: true }).click()
    await expect(page.getByText('Initialize project?', { exact: true })).toHaveCount(0)
    expect(await readdir(projectPath)).toEqual([])
    await expect(page.getByText(projectPath, { exact: true })).toHaveCount(0)

    await mkdir(join(projectPath, '.kb'))
    await page.getByRole('button', { name: 'Add project', exact: true }).click()
    await page.getByLabel('Folder path').fill(projectPath)
    await page.getByRole('button', { name: 'Register', exact: true }).click()
    await expect(page.getByText(projectPath, { exact: true })).toBeVisible()
    await expect(page.getByText(`Registered ${projectPath}`, { exact: true })).toBeVisible()

    const row = page.getByRole('row').filter({ hasText: projectName })
    await row.getByRole('button', { name: 'Remove', exact: true }).click()
    await page.getByRole('button', { name: 'Unregister', exact: true }).click()
    await expect(page.getByText(`Unregistered ${projectName}; files were not touched.`, { exact: true })).toBeVisible()
    await expect(row).toHaveCount(0)
    expect(await readdir(join(projectPath, '.kb'))).toEqual([])
  } finally {
    await rm(projectPath, { recursive: true, force: true })
  }
})

test('S-121.1: analytics overview summarizes events and adapter usage', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')

  await page.goto(`${GATEWAY}/analytics/overview`)
  await expect(page.getByRole('heading', { name: 'Analytics Overview', exact: true })).toBeVisible()
  await expect(page.getByText('Total Events', { exact: true })).toBeVisible()
  await expect(page.getByText('Event Types', { exact: true })).toBeVisible()
  await expect(page.getByText('Sources', { exact: true })).toBeVisible()
  await expect(page.getByText('Actors', { exact: true })).toBeVisible()
  await expect(page.getByText('LLM Usage', { exact: true })).toBeVisible()
  await expect(page.getByText('Embeddings', { exact: true })).toBeVisible()
  await expect(page.getByText('VectorStore', { exact: true })).toBeVisible()
  await expect(page.getByText('Cache', { exact: true })).toBeVisible()
  await expect(page.getByText('Storage', { exact: true })).toBeVisible()
})

test('S-121.6: unconfigured analytics shows adapter setup guidance', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  const projectId = process.env.KB_STUDIO_PROJECT_ID
  expect(projectId, 'KB_STUDIO_PROJECT_ID must identify the registered project').toBeTruthy()
  await page.addInitScript((id) => localStorage.setItem('kb-studio-active-project', id), projectId!)
  await page.route(`**/api/v1/projects/${encodeURIComponent(projectId!)}/analytics/stats`, async (route) => {
    await route.fulfill({ status: 501, contentType: 'application/json', body: JSON.stringify({ message: 'Analytics adapter does not support reading stats' }) })
  })

  await page.goto(`${GATEWAY}/analytics/overview`)
  await expect(page.getByText('Analytics Not Configured', { exact: true })).toBeVisible()
  await expect(page.getByText('Configure an analytics adapter in kb.config.json', { exact: false })).toBeVisible()
})

test('S-125.5: unavailable analytics service names its source and recovery command', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  const projectId = process.env.KB_STUDIO_PROJECT_ID
  expect(projectId, 'KB_STUDIO_PROJECT_ID must identify the registered project').toBeTruthy()
  await page.addInitScript((id) => localStorage.setItem('kb-studio-active-project', id), projectId!)
  await page.route(`**/api/v1/projects/${encodeURIComponent(projectId!)}/analytics/stats`, async (route) => {
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ message: 'unavailable' }) })
  })

  await page.goto(`${GATEWAY}/analytics/overview`)
  await expect(page.getByText('Analytics source unavailable', { exact: true })).toBeVisible()
  await expect(page.getByText('Source: REST API analytics endpoint.', { exact: false })).toBeVisible()
  await expect(page.getByText('kb-create doctor --platform-root <platform-root>', { exact: false })).toBeVisible()
})

test('S-120.7 and S-125.4: unavailable gateway keeps Dashboard visible with recovery commands', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  await page.route('**/api/observability/health', async (route) => {
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ message: 'gateway offline' }) })
  })
  await page.route('**/api/ready', async (route) => {
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ ready: false, reason: 'gateway unavailable' }) })
  })

  await page.goto(`${GATEWAY}/`)
  await expect(page.getByRole('heading', { name: 'Intelligence Dashboard', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible()
  await expect(page.getByText('Gateway health is unavailable', { exact: true })).toBeVisible()
  await expect(page.getByText('Gateway unavailable', { exact: true })).toBeVisible()
  await expect(page.getByText('kb-create status --platform-root <platform-root>', { exact: false })).toBeVisible()
  await expect(page.getByText('kb-create doctor --platform-root <platform-root>', { exact: false })).toBeVisible()
  await expect(page.getByRole('progressbar')).toHaveCount(0)
})

test('S-122.2 and S-122.3: failed workflow detail carries its project and run to Logs', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  const projectId = process.env.KB_STUDIO_PROJECT_ID
  expect(projectId, 'KB_STUDIO_PROJECT_ID must identify the registered project').toBeTruthy()
  await page.addInitScript((id) => localStorage.setItem('kb.workflow.projectId', id), projectId!)
  await page.route(`**/api/v1/projects/${encodeURIComponent(projectId!)}/workflow/api/v1/runs/run-studio-diagnostic`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        run: {
          id: 'run-studio-diagnostic',
          name: 'studio-diagnostic-fixture',
          version: 1,
          status: 'failed',
          jobs: [{
            id: 'job-diagnostic',
            jobName: 'diagnostics',
            status: 'failed',
            steps: [{ id: 'step-diagnostic', name: 'diagnostic failure', status: 'failed', error: { message: 'STUDIO_DIAGNOSTIC_MARKER' } }],
          }],
        },
      }),
    })
  })

  await page.goto(`${GATEWAY}/p/workflows/runs/run-studio-diagnostic`)
  const logsLink = page.getByRole('link', { name: 'Inspect run logs', exact: true })
  await expect(logsLink).toBeVisible()
  await expect(logsLink).toHaveAttribute('href', `/observability/logs?projectId=${encodeURIComponent(projectId!)}&executionId=run-studio-diagnostic`)
  const logsRequest = page.waitForRequest(request => request.url().includes(`/api/v1/projects/${encodeURIComponent(projectId!)}/logs/stream`))
  await logsLink.click()
  await expect(page).toHaveURL(new RegExp(`/observability/logs\\?projectId=${encodeURIComponent(projectId!)}&executionId=run-studio-diagnostic$`))
  expect((await logsRequest).url()).toContain('executionId=run-studio-diagnostic')
})

test('S-122.5: unavailable observability logs name the source and doctor command', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  const projectId = process.env.KB_STUDIO_PROJECT_ID
  expect(projectId, 'KB_STUDIO_PROJECT_ID must identify the registered project').toBeTruthy()
  await page.addInitScript((id) => localStorage.setItem('kb-studio-active-project', id), projectId!)
  await page.route(`**/api/v1/projects/${encodeURIComponent(projectId!)}/logs**`, async (route) => {
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ message: 'unavailable' }) })
  })

  await page.goto(`${GATEWAY}/observability/logs?projectId=${encodeURIComponent(projectId!)}&executionId=run-studio-diagnostic`)
  await page.getByText('History', { exact: true }).click()
  await expect(page.getByText('Observability logs unavailable', { exact: true })).toBeVisible()
  await expect(page.getByText('Source: REST API observability logs.', { exact: false })).toBeVisible()
  await expect(page.getByText('kb-create doctor --platform-root <platform-root>', { exact: false })).toBeVisible()
})

test('S-123.2 and S-123.4: Commit page reports the missing LLM adapter with a bind command', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed Studio journey hook')
  await page.route('**/api/v1/plugins/commit/scopes**', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ value: 'root', options: [{ label: 'root', value: 'root' }] }) })
  })
  await page.route('**/api/v1/plugins/commit/status**', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ filesChanged: 1, hasPlan: false, planStatus: 'idle' }) })
  })
  await page.route('**/api/v1/plugins/commit/plan**', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plan: { commits: [] } }) })
  })
  await page.route('**/api/v1/plugins/commit/generate**', async (route) => {
    await route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ ok: false, error: { code: 'KB_ADAPTER_NOT_CONFIGURED', message: 'Service llm is not configured.' } }),
    })
  })

  await page.goto(`${GATEWAY}/p/commit`)
  await expect(page.getByRole('tab', { name: 'Commit Plan', exact: true })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Files', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Generate plan', exact: true }).click()
  await expect(page.getByText('LLM adapter required', { exact: true })).toBeVisible()
  await expect(page.getByText('kb adapter bind llm @kb-labs/adapters-openai', { exact: false })).toBeVisible()
})
