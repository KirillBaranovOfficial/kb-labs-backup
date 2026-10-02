import { test, expect } from '@playwright/test'

const GATEWAY = (process.env.GATEWAY_URL ?? 'http://127.0.0.1:4000').replace(/\/+$/, '')
const enabled = process.env.KB_STUDIO_ANALYTICS_JOURNEY === '1'

function requireFixture(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`Analytics Studio journey requires ${name} from the installed journey fixture`)
  return value
}

test('Analytics filters and event details @S-121.2 @S-121.3 @S-121.4 @S-121.5', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed analytics journey hook')
  const eventType = requireFixture('KB_STUDIO_ANALYTICS_EVENT_TYPE')
  const source = requireFixture('KB_STUDIO_ANALYTICS_SOURCE')
  const model = requireFixture('KB_STUDIO_ANALYTICS_MODEL')
  const eventId = requireFixture('KB_STUDIO_ANALYTICS_EVENT_ID')
  const projectId = requireFixture('KB_STUDIO_PROJECT_ID')
  await page.addInitScript(id => localStorage.setItem('kb-studio-active-project', id), projectId)

  await page.goto(`${GATEWAY}/analytics/events`)
  await expect(page.getByRole('heading', { name: 'Event Log', exact: true })).toBeVisible()
  await page.getByRole('combobox').nth(0).click()
  await page.getByText(eventType, { exact: true }).last().click()
  await page.getByRole('combobox').nth(1).click()
  await page.getByText(source, { exact: true }).last().click()
  const filteredEvents = page.waitForResponse(response => response.url().includes('/analytics/events?')
    && response.url().includes(`type=${encodeURIComponent(eventType)}`)
    && response.url().includes(`source=${encodeURIComponent(source)}`))
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  expect((await filteredEvents).ok()).toBe(true)
  const rows = await page.locator('tbody tr').allTextContents()
  expect(rows.length).toBeGreaterThan(0)
  for (const row of rows) {
    expect(row).toContain(eventType)
    expect(row).toContain(source)
  }
  await page.getByText(eventType, { exact: true }).first().click()
  await expect(page.getByText('Payload', { exact: true })).toBeVisible()
  await expect(page.getByText(source, { exact: true }).last()).toBeVisible()
  await expect(page.getByText(eventId, { exact: false })).toBeVisible()

  await page.goto(`${GATEWAY}/analytics/llm`)
  await expect(page.getByRole('heading', { name: 'LLM Usage Analytics', exact: true })).toBeVisible()
  const sevenDayRequest = page.waitForResponse(response => response.url().includes('/adapters/llm/usage?')
    && response.url().includes('from=') && response.url().includes('to='))
  await page.getByRole('combobox').first().click()
  await page.getByText('Last 7 days', { exact: true }).click()
  expect((await sevenDayRequest).ok()).toBe(true)
  await expect(page.getByText('Daily Usage Trend', { exact: true })).toBeVisible()
  const modelRequest = page.waitForResponse(response => response.url().includes('/adapters/llm/daily-stats?')
    && response.url().includes(`models=${encodeURIComponent(model)}`))
  await page.getByRole('combobox').nth(1).click()
  await page.getByText(model, { exact: true }).last().click()
  expect((await modelRequest).ok()).toBe(true)
  const allModelsRequest = page.waitForResponse(response => response.url().includes('/adapters/llm/daily-stats?')
    && !new URL(response.url()).searchParams.has('models'))
  await page.locator('.ant-select-selection-item-remove').last().click()
  expect((await allModelsRequest).ok()).toBe(true)
})

test('Failed workflow is scoped and observability identifies the unhealthy runtime @S-122.1 @S-122.4', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed analytics journey hook')
  const projectName = requireFixture('KB_STUDIO_DIAGNOSTIC_PROJECT')
  const runId = requireFixture('KB_STUDIO_DIAGNOSTIC_RUN_ID')
  const healthyRunId = requireFixture('KB_STUDIO_HEALTHY_PROJECT_RUN_ID')
  const unhealthyRuntime = requireFixture('KB_STUDIO_UNHEALTHY_RUNTIME')
  const healthError = requireFixture('KB_STUDIO_RUNTIME_ERROR')

  await page.goto(`${GATEWAY}/workflows/runs`)
  await page.getByRole('combobox', { name: /project/i }).selectOption({ label: projectName })
  const runRow = page.getByRole('row').filter({ hasText: runId })
  await expect(runRow).toContainText('Failed')
  await expect(page.getByRole('row').filter({ hasText: healthyRunId })).toHaveCount(0)
  await page.goto(`${GATEWAY}/observability/metrics`)
  await expect(page.getByText(unhealthyRuntime, { exact: true })).toBeVisible()
  await expect(page.getByText(/unhealthy|degraded/i)).toBeVisible()
  await expect(page.getByText(healthError, { exact: false })).toBeVisible()
  await expect(page.getByText(projectName, { exact: true })).toBeVisible()
})

test('Supported platform settings persist and validate @S-183.1 @S-183.2 @S-183.3 @S-183.4 @S-183.5', async ({ page, request }) => {
  test.skip(!enabled, 'Enabled by the installed analytics journey hook')
  const adminToken = requireFixture('KB_STUDIO_ADMIN_TOKEN')
  const memberToken = requireFixture('KB_STUDIO_MEMBER_TOKEN')
  await page.goto(`${GATEWAY}/settings`)
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()
  await expect(page.getByLabel('platform.execution.mode')).toBeVisible()
  await expect(page.getByLabel('platform.core.workflows.maxConcurrent')).toBeVisible()
  await expect(page.getByLabel(/adapter bindings/i)).toHaveCount(0)
  await page.getByLabel('platform.execution.mode').selectOption('worker-pool')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await page.reload()
  await expect(page.getByLabel('platform.execution.mode')).toHaveValue('worker-pool')
  await page.getByLabel('platform.core.workflows.maxConcurrent').fill('4')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await page.reload()
  await expect(page.getByLabel('platform.core.workflows.maxConcurrent')).toHaveValue('4')
  await page.getByLabel('platform.execution.mode').fill('turbo')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText(/platform\.execution\.mode/i)).toBeVisible()
  await expect(page.getByLabel('platform.execution.mode')).toHaveValue('worker-pool')

  const mutation = await request.put(`${GATEWAY}/api/v1/settings/platform.execution.mode`, {
    headers: { Authorization: `Bearer ${memberToken}` }, data: { value: 'inline' },
  })
  expect([401, 403]).toContain(mutation.status())
  expect(adminToken).toBeTruthy()
})

test('Project analytics and dashboard remain isolated @S-201.1 @S-201.2 @S-201.4', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the installed analytics journey hook')
  const aName = requireFixture('KB_STUDIO_PROJECT_A_NAME')
  const bName = requireFixture('KB_STUDIO_PROJECT_B_NAME')
  const aMarker = requireFixture('KB_STUDIO_PROJECT_A_EVENT')
  const bMarker = requireFixture('KB_STUDIO_PROJECT_B_EVENT')
  const selectProject = async (name: string) => {
    await page.getByRole('combobox', { name: /project/i }).selectOption({ label: name })
    await expect(page.getByText(name, { exact: true })).toBeVisible()
  }

  await page.goto(`${GATEWAY}/`)
  await selectProject(aName)
  for (const path of ['/', '/analytics/overview', '/analytics/events', '/observability/logs']) {
    await page.goto(`${GATEWAY}${path}`)
    await expect(page.getByText(aName, { exact: true })).toBeVisible()
    await expect(page.getByText(aMarker, { exact: false })).toBeVisible()
    await expect(page.getByText(bMarker, { exact: false })).toHaveCount(0)
  }
  await page.goto(`${GATEWAY}/`)
  await selectProject(bName)
  for (const path of ['/', '/analytics/overview', '/analytics/events', '/observability/logs']) {
    await page.goto(`${GATEWAY}${path}`)
    await expect(page.getByText(bName, { exact: true })).toBeVisible()
    await expect(page.getByText(bMarker, { exact: false })).toBeVisible()
    await expect(page.getByText(aMarker, { exact: false })).toHaveCount(0)
  }
  await page.goto(`${GATEWAY}/`)
  await selectProject(aName)
  await expect(page.getByText(aMarker, { exact: false })).toBeVisible()
  await expect(page.getByText(bMarker, { exact: false })).toHaveCount(0)
})
