import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockContext } from '@kb-labs/shared-testing-e2e/cli';
import type { RestInput } from '@kb-labs/sdk';

vi.mock('../../http-client.js', () => ({
  getWorkflowDaemonUrl: () => 'http://127.0.0.1:4000/api/v1/projects/prj_0123456789abcdef/workflow',
  fetchWorkflowApi: (url: string, init?: RequestInit) => fetch(url, init),
}));

import handler from '../../rest/workflow-run-handler.js';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ ok: true, data: { runId: 'run-123', status: 'queued' } }),
    { status: 200, headers: { 'content-type': 'application/json' } },
  )));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('workflow-run-handler (REST)', () => {
  it('posts a run through the project workflow endpoint', async () => {
    const input: RestInput<{ input: Record<string, unknown> }, unknown, { id: string }> = {
      params: { id: 'hello' },
      body: { input: { name: 'world' } },
      query: undefined,
    };

    await expect(handler.execute(createMockContext(), input)).resolves.toEqual({
      runId: 'run-123',
      status: 'queued',
    });
    expect(fetch).toHaveBeenCalledWith(
      'http://127.0.0.1:4000/api/v1/projects/prj_0123456789abcdef/workflow/api/v1/workflows/hello/runs',
      expect.objectContaining({ method: 'POST' }),
    );
  });
});
