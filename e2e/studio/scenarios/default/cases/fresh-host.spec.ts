import { test, expect } from '@playwright/test'

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

test('fresh host Studio journey: local access or secured admin and member access', async ({ page, browser }) => {
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
