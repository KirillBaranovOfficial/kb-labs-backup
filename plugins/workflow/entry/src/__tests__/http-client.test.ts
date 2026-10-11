import { afterEach, describe, expect, it, vi } from 'vitest';
import { deriveProjectId } from '@kb-labs/core-project-registry';

vi.mock('@kb-labs/sdk', () => ({
  useEnv: (name: string) => process.env[name],
  getHostServiceAddress: (_service: string, environment: Record<string, string | undefined>) => {
    if (!environment.KB_GATEWAY_URL) { throw new Error('gateway address missing'); }
    return environment.KB_GATEWAY_URL.replace(/\/+$/, '');
  },
}));

// Saved sessions/credentials live in the developer's real HOME; a stale login
// would refresh through the stubbed fetch. These client tests run logged out.
vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  gatewayApiFetch: (input: string | URL, init?: RequestInit) => fetch(input, init),
}));

import { getWorkflowDaemonUrl, WorkflowDaemonClient } from '../http-client.js';

describe('workflow project gateway address', () => {
  afterEach(() => {
    delete process.env.KB_GATEWAY_URL;
    delete process.env.KB_PROJECT_ID;
  });

  it('routes through the selected project runtime', () => {
    process.env.KB_GATEWAY_URL = 'http://127.0.0.1:4312/';
    process.env.KB_PROJECT_ID = 'prj_0123456789abcdef';

    expect(getWorkflowDaemonUrl()).toBe(
      'http://127.0.0.1:4312/api/v1/projects/prj_0123456789abcdef/workflow',
    );
  });

  it('does not fall back to a default gateway address when none is injected', () => {
    delete process.env.KB_PROJECT_ID;
    expect(() => getWorkflowDaemonUrl()).toThrow('gateway address missing');
  });

  it('lists cron schedules through the daemon plural route', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: true,
      data: { crons: [{ id: 'user:journey-cron', schedule: '* * * * *', enabled: true }] },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);
    try {
      const client = new WorkflowDaemonClient({ url: 'http://127.0.0.1:4312/workflow' });
      await expect(client.getCronJobs()).resolves.toMatchObject({
        crons: [{ id: 'user:journey-cron', schedule: '* * * * *', enabled: true }],
      });
      expect(fetch).toHaveBeenCalledWith('http://127.0.0.1:4312/workflow/crons', undefined);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
