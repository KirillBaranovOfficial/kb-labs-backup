import { describe, expect, it, vi } from 'vitest'
import { HttpWorkflowSource } from '../http-workflow-source'
import { KBError } from '../../errors/kb-error'
import { HttpClient } from '../../client/http-client'

class StubClient extends HttpClient {
  override fetch = vi.fn() as HttpClient['fetch'] & ReturnType<typeof vi.fn>
}

const SAMPLE_RUN = { id: 'run-1', status: 'queued' }
const PROJECT_ID = 'project-a'

function makeSource(client: StubClient): HttpWorkflowSource {
  return new HttpWorkflowSource(client, PROJECT_ID)
}

describe('HttpWorkflowSource', () => {
  // --- listRuns ---
  it('lists runs via plugin route with query filters', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ runs: [], total: 0 })
    const source = makeSource(client)

    await source.listRuns({ status: 'running', limit: 5 })

    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/runs?status=running&limit=5', { baseURL: '' },
    )
  })

  it('lists runs without filters', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ runs: [], total: 0 })
    const source = makeSource(client)

    await source.listRuns()

    expect(client.fetch).toHaveBeenCalledWith('/api/v1/projects/project-a/workflow/runs', { baseURL: '' })
  })

  // --- getRun ---
  it('gets run via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ run: SAMPLE_RUN })
    const source = makeSource(client)

    const result = await source.getRun('run-1')

    expect(result).toEqual(SAMPLE_RUN)
    expect(client.fetch).toHaveBeenCalledWith('/api/v1/projects/project-a/workflow/runs/run-1', { baseURL: '' })
  })

  it('returns null when run is not found', async () => {
    const client = new StubClient()
    client.fetch.mockRejectedValue(new KBError('NOT_FOUND', 'missing', 404))
    const source = makeSource(client)

    const run = await source.getRun('unknown')

    expect(run).toBeNull()
  })

  it('throws non-404 errors from getRun', async () => {
    const client = new StubClient()
    client.fetch.mockRejectedValue(new KBError('INTERNAL', 'boom', 500))
    const source = makeSource(client)

    await expect(source.getRun('run-1')).rejects.toThrow()
  })

  // --- cancelRun ---
  it('cancels run via plugin route and fetches updated run', async () => {
    const client = new StubClient()
    // First call: cancel, second call: getRun
    client.fetch
      .mockResolvedValueOnce({ cancelled: true, runId: 'run-1' })
      .mockResolvedValueOnce({ run: { ...SAMPLE_RUN, status: 'cancelled' } })
    const source = makeSource(client)

    const result = await source.cancelRun('run-1')

    expect(client.fetch).toHaveBeenNthCalledWith(
      1,
      '/api/v1/projects/project-a/workflow/runs/run-1/cancel',
      { method: 'POST', baseURL: '' },
    )
    expect(result.status).toBe('cancelled')
  })

  it('returns fallback when cancelled run is not found', async () => {
    const client = new StubClient()
    client.fetch
      .mockResolvedValueOnce({ cancelled: true, runId: 'run-1' })
      .mockRejectedValueOnce(new KBError('NOT_FOUND', 'missing', 404))
    const source = makeSource(client)

    const result = await source.cancelRun('run-1')

    expect(result.id).toBe('run-1')
    expect(result.status).toBe('cancelled')
  })

  // --- runWorkflow ---
  it('runs workflow via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ run: SAMPLE_RUN })
    const source = makeSource(client)

    const result = await source.runWorkflow({
      spec: { name: 'demo', version: '1.0.0', jobs: {} },
      metadata: { trigger: 'ci' },
    })

    expect(result).toEqual(SAMPLE_RUN)
    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/workflows/demo/run',
      {
        method: 'POST',
        data: { input: { trigger: 'ci' } },
        baseURL: '',
      },
    )
  })

  it('routes the REST-owned run event stream through the selected project REST service', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ events: [], cursor: null })
    const workflow = makeSource(client)

    await workflow.listEvents('run/1', { cursor: 'event-1', limit: 5 })

    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/rest/workflows/runs/run%2F1/events?cursor=event-1&limit=5',
      { baseURL: '' },
    )
  })

  // --- getStats ---
  it('gets stats via plugin route', async () => {
    const client = new StubClient()
    const stats = { workflows: { total: 5 } }
    client.fetch.mockResolvedValue(stats)
    const source = makeSource(client)

    const result = await source.getStats()

    expect(result).toEqual(stats)
    expect(client.fetch).toHaveBeenCalledWith('/api/v1/projects/project-a/workflow/stats', { baseURL: '' })
  })

  // --- listWorkflows ---
  it('lists workflows via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ workflows: [] })
    const source = makeSource(client)

    await source.listWorkflows({ limit: 10 })

    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/workflows?limit=10', { baseURL: '' },
    )
  })

  // --- getWorkflow ---
  it('returns null when workflow not found', async () => {
    const client = new StubClient()
    client.fetch.mockRejectedValue(new KBError('NOT_FOUND', 'missing', 404))
    const source = makeSource(client)

    const result = await source.getWorkflow('unknown')

    expect(result).toBeNull()
  })

  // --- runWorkflowById ---
  it('runs workflow by ID via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ runId: 'r-1', status: 'queued' })
    const source = makeSource(client)

    const result = await source.runWorkflowById('my-wf', { key: 'val' })

    expect(result).toEqual({ runId: 'r-1', status: 'queued' })
    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/workflows/my-wf/run',
      { method: 'POST', data: { input: { key: 'val' } }, baseURL: '' },
    )
  })

  // --- listJobs ---
  it('lists jobs with filters', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ jobs: [] })
    const source = makeSource(client)

    await source.listJobs({ type: 'build', status: 'running', limit: 10, offset: 5 })

    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/jobs?type=build&status=running&limit=10&offset=5', { baseURL: '' },
    )
  })

  // --- getJob ---
  it('returns null when job not found', async () => {
    const client = new StubClient()
    client.fetch.mockRejectedValue(new KBError('NOT_FOUND', 'missing', 404))
    const source = makeSource(client)

    const result = await source.getJob('j-unknown')

    expect(result).toBeNull()
  })

  // --- listCronJobs ---
  it('lists cron jobs via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ crons: [] })
    const source = makeSource(client)

    await source.listCronJobs()

    expect(client.fetch).toHaveBeenCalledWith('/api/v1/projects/project-a/workflow/crons', { baseURL: '' })
  })

  // --- getPendingApprovals ---
  it('gets pending approvals via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ runId: 'r-1', pending: [] })
    const source = makeSource(client)

    const result = await source.getPendingApprovals('r-1')

    expect(result).toEqual({ runId: 'r-1', pending: [] })
    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/runs/r-1/approvals', { baseURL: '' },
    )
  })

  // --- resolveApproval ---
  it('resolves approval via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ resolved: true })
    const source = makeSource(client)

    const result = await source.resolveApproval({
      runId: 'r-1',
      jobId: 'j-1',
      stepId: 's-1',
      action: 'approve',
      comment: 'LGTM',
    })

    expect(result).toEqual({ resolved: true })
    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/runs/r-1/approvals/resolve',
      {
        method: 'POST',
        data: {
          jobId: 'j-1',
          stepId: 's-1',
          action: 'approve',
          comment: 'LGTM',
        },
        baseURL: '',
      },
    )
  })

  // --- cancelWorkflowRun ---
  it('cancels workflow run via plugin route', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ cancelled: true, runId: 'r-1' })
    const source = makeSource(client)

    const result = await source.cancelWorkflowRun('r-1')

    expect(result).toEqual({ cancelled: true, runId: 'r-1' })
    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/runs/r-1/cancel',
      { method: 'POST', baseURL: '' },
    )
  })

  // --- getWorkflowRuns ---
  it('gets workflow runs history with filters', async () => {
    const client = new StubClient()
    client.fetch.mockResolvedValue({ workflowId: 'wf-1', runs: [], total: 0 })
    const source = makeSource(client)

    await source.getWorkflowRuns('wf-1', { limit: 10, offset: 0, status: 'success' })

    expect(client.fetch).toHaveBeenCalledWith(
      '/api/v1/projects/project-a/workflow/workflows/wf-1/runs?limit=10&offset=0&status=success', { baseURL: '' },
    )
  })
})
