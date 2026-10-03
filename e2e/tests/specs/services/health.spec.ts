import { test, expect } from '@playwright/test'
import { GATEWAY, REST, MARKETPLACE, WORKFLOW, STATE } from '../../fixtures/urls.js'
import { decodeUxSuccessResult } from '@kb-labs/core-platform/ux-result'

const services = [
  { id: 'S-01', name: 'gateway',      url: GATEWAY,     path: '/health' },
  { id: 'S-02', name: 'rest-api',     url: REST,        path: '/health' },
  { id: 'S-03', name: 'marketplace',  url: MARKETPLACE, path: '/health' },
  { id: 'S-04', name: 'workflow',     url: WORKFLOW,    path: '/health' },
  { id: 'S-05', name: 'state-daemon', url: STATE,       path: '/health' },
]

for (const svc of services) {
  test(`${svc.id}: ${svc.name} is healthy`, async ({ request }) => {
    const res = await request.get(`${svc.url}${svc.path}`)
    expect(res.status()).toBe(200)
    const payload = await res.json()
    const status = svc.name === 'gateway' || svc.name === 'rest-api'
      ? decodeUxSuccessResult<{ status: string }>(payload).status
      : (payload as { status: string }).status
    expect(status).toMatch(/ok|healthy|ready/)
  })
}
