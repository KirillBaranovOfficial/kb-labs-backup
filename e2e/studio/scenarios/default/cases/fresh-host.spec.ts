import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'

const GATEWAY = (process.env.GATEWAY_URL ?? 'http://127.0.0.1:4000').replace(/\/+$/, '')

const enabled = process.env.KB_STUDIO_FRESH_HOST_JOURNEY === '1'
const mode = process.env.KB_ACCESS_MODE ?? 'local'
const projectId = process.env.KB_STUDIO_PROJECT_ID
const projectName = process.env.KB_STUDIO_PROJECT_NAME

async function signIn(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto(`${GATEWAY}/`)
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: /sign in|log in/i }).click()
  await expect(page).toHaveURL(`${GATEWAY}/`)
}

test('S-020.1-.7 and S-125.1-.3: fresh host Studio dashboard, navigation, session, and access controls', async ({ page, browser }) => {
  test.skip(!enabled, 'Enabled by the fresh-install journey hook')
  expect(projectId, 'KB_STUDIO_PROJECT_ID must identify the project registered by the fresh-install journey').toBeTruthy()
  expect(projectName, 'KB_STUDIO_PROJECT_NAME must identify the project registered by the fresh-install journey').toBeTruthy()

  await test.step('open Studio in the configured access mode', async () => {
    await page.goto(`${GATEWAY}/`)
    if (mode === 'secured') {
      const email = process.env.KB_STUDIO_ADMIN_EMAIL
      const password = process.env.KB_STUDIO_ADMIN_PASSWORD
      expect(email, 'KB_STUDIO_ADMIN_EMAIL is required in secured mode').toBeTruthy()
      expect(password, 'KB_STUDIO_ADMIN_PASSWORD is required in secured mode').toBeTruthy()
      await expect(page).toHaveURL(/\/login$/)
      await page.getByLabel('Email').fill(email!)
      await page.getByLabel('Password').fill('incorrect-journey-password')
      await page.getByRole('button', { name: /sign in|log in/i }).click()
      await expect(page.getByText('Invalid credentials', { exact: true })).toBeVisible()
      await signIn(page, email!, password!)
    } else {
      await expect(page).not.toHaveURL(/\/login$/)
    }
    await expect(page.getByRole('link', { name: 'Projects' })).toBeVisible()
  })

  await test.step('show the registered project and healthy gateway', async () => {
    await page.goto(`${GATEWAY}/projects`)
    await expect(page.getByText(projectName!, { exact: true })).toBeVisible()
    await page.goto(`${GATEWAY}/`)
    await expect(page.getByText('Healthy', { exact: true })).toBeVisible()
    if (mode === 'secured') {
      await page.reload()
      await expect(page.getByText(process.env.KB_STUDIO_ADMIN_EMAIL!, { exact: true }).first()).toBeVisible()
      await expect(page.getByText('Healthy', { exact: true })).toBeVisible()
    }
  })

  await test.step('navigate to first-tier pages from Studio navigation', async () => {
    await page.goto(`${GATEWAY}/projects`)
    await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
    await page.goto(`${GATEWAY}/analytics/overview`)
    await expect(page.getByRole('heading', { name: 'Analytics Overview', exact: true })).toBeVisible()
    await page.goto(`${GATEWAY}/observability/logs`)
    await expect(page.getByRole('heading', { name: 'Logs', exact: true })).toBeVisible()
  })

  await test.step('list the commit plugin for the active project', async () => {
    const response = await page.request.get(`${GATEWAY}/api/v1/plugins?projectId=${encodeURIComponent(projectId!)}`)
    expect(response.status()).toBe(200)
    const payload = await response.json() as { data?: Array<{ id?: string }> }
    expect(payload.data?.some(plugin => plugin.id?.includes('commit'))).toBe(true)
    await page.goto(`${GATEWAY}/plugins`)
    await expect(page.getByText(/commit/i).first()).toBeVisible()
  })

  if (mode === 'secured') {
    await test.step('logout returns the browser to the login form', async () => {
      await page.getByRole('button', { name: new RegExp(process.env.KB_STUDIO_ADMIN_EMAIL!, 'i') }).click()
      await page.getByRole('menuitem', { name: 'Logout', exact: true }).click()
      await expect(page).toHaveURL(/\/login$/)
      await page.goto(`${GATEWAY}/`)
      await expect(page).toHaveURL(/\/login$/)
    })

    await test.step('[S-108.1] member has no Users or Invites navigation', async () => {
      const email = process.env.KB_STUDIO_MEMBER_EMAIL
      const password = process.env.KB_STUDIO_MEMBER_PASSWORD
      expect(email, 'KB_STUDIO_MEMBER_EMAIL is required in secured mode').toBeTruthy()
      expect(password, 'KB_STUDIO_MEMBER_PASSWORD is required in secured mode').toBeTruthy()
      const memberContext = await browser.newContext()
      const memberPage = await memberContext.newPage()
      await signIn(memberPage, email!, password!)
      await expect(memberPage.getByRole('link', { name: 'Settings' })).toHaveCount(0)
      await expect(memberPage.getByRole('link', { name: 'Users' })).toHaveCount(0)
      await expect(memberPage.getByRole('link', { name: 'Invites' })).toHaveCount(0)
      await test.step('[S-108.2] deny direct user and invite administration routes', async () => {
        await memberPage.goto(`${GATEWAY}/admin/users`)
        await expect(memberPage.getByTestId('forbidden-page')).toBeVisible()
        await expect(memberPage.getByText('403 — Forbidden')).toBeVisible()
        await expect(memberPage.getByText('You do not have permission to view this page.')).toBeVisible()
        await memberPage.goto(`${GATEWAY}/admin/invites`)
        await expect(memberPage.getByTestId('forbidden-page')).toBeVisible()
        await expect(memberPage.getByText('403 — Forbidden')).toBeVisible()
        await expect(memberPage.getByText('You do not have permission to view this page.')).toBeVisible()
      })
      await test.step('[S-108.3] keep member project access', async () => {
        await memberPage.goto(`${GATEWAY}/projects`)
        await expect(memberPage.getByText(projectName!, { exact: true })).toBeVisible()
      })
      await memberContext.close()
    })
  }
})

async function openCrons(page: import('@playwright/test').Page) {
  await page.addInitScript((selectedProjectId: string | undefined) => {
    if (selectedProjectId) localStorage.setItem('kb.workflow.projectId', selectedProjectId)
  }, projectId)
  await page.goto(`${GATEWAY}/`)
  if (mode === 'secured') {
    await signIn(page, process.env.KB_STUDIO_ADMIN_EMAIL!, process.env.KB_STUDIO_ADMIN_PASSWORD!)
  }
  await page.goto(`${GATEWAY}/p/workflows/crons`)
  await expect(page.getByText('Workflow Crons', { exact: true })).toBeVisible()
}

test('Studio schedules @S-055.1 shows user and read-only plugin crons', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the fresh-install journey hook')
  await openCrons(page)
  const cronId = process.env.KB_STUDIO_CRON_ID!
  await expect(page.getByText(cronId, { exact: true })).toBeVisible()
  for (const column of ['Schedule', 'Target', 'Status', 'Last Run', 'Next Run', 'Source', 'Action']) {
    await expect(page.getByRole('columnheader', { name: column })).toBeVisible()
  }
  const userRow = page.getByRole('row').filter({ hasText: cronId })
  await expect(userRow).toContainText('0 0 * * *')
  await expect(userRow).toContainText('journey-hello')
  await expect(userRow).toContainText('Enabled')
  await expect(userRow.locator('td').nth(5)).toContainText('-')
  await expect(userRow.locator('td').nth(6)).not.toContainText('-')
  const pluginRow = page.getByRole('row').filter({ hasText: 'Read only' })
  await expect(pluginRow).toContainText('plugin')
  await expect(pluginRow.locator('td').nth(1)).not.toBeEmpty()
  await expect(pluginRow.locator('td').nth(2)).not.toBeEmpty()
  await expect(pluginRow.locator('td').nth(6)).not.toBeEmpty()
})

test('Studio schedules @S-055.2 toggles a project cron', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the fresh-install journey hook')
  await openCrons(page)
  const row = page.getByRole('row').filter({ hasText: process.env.KB_STUDIO_CRON_ID! })
  await row.getByRole('button', { name: 'Disable' }).click()
  await expect(row.getByText('Disabled', { exact: true })).toBeVisible()
  const response = await page.request.get(`${GATEWAY}/api/v1/projects/${encodeURIComponent(projectId!)}/workflow/api/v1/crons`)
  expect(response.status()).toBe(200)
  const payload = await response.json() as { data?: { crons?: Array<{ id: string; enabled: boolean }> } }
  expect(payload.data?.crons?.find(cron => cron.id === process.env.KB_STUDIO_CRON_ID)?.enabled).toBe(false)
})

test('Studio schedules @S-055.3 reflects a CLI disable', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the fresh-install journey hook')
  await openCrons(page)
  execFileSync('kb', ['workflow', 'crons', 'disable', process.env.KB_STUDIO_CRON_ID!], {
    cwd: process.env.KB_STUDIO_CRON_PROJECT_ROOT,
    env: process.env,
    stdio: 'pipe',
  })
  await page.reload()
  const row = page.getByRole('row').filter({ hasText: process.env.KB_STUDIO_CRON_ID! })
  await expect(row.getByText('Disabled', { exact: true })).toBeVisible()
})

test('Studio schedules @S-055.4 isolates schedules by project', async ({ page }) => {
  test.skip(!enabled, 'Enabled by the fresh-install journey hook')
  await openCrons(page)
  await page.getByLabel('Workflow project').selectOption(process.env.KB_STUDIO_PROJECT_B_ID!)
  await expect(page.getByText(process.env.KB_STUDIO_PROJECT_B_CRON_ID!, { exact: true })).toBeVisible()
  for (const cronId of (process.env.KB_STUDIO_PROJECT_A_USER_CRON_IDS ?? '').split(',').filter(Boolean)) {
    await expect(page.getByText(cronId, { exact: true })).toHaveCount(0)
  }
  for (const cronId of (process.env.KB_STUDIO_PROJECT_A_PLUGIN_CRON_IDS ?? '').split(',').filter(Boolean)) {
    await expect(page.getByText(cronId, { exact: true })).toBeVisible()
  }
})
