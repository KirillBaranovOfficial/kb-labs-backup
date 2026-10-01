import { afterEach, describe, expect, it, vi } from 'vitest';

const gatewayApiFetch = vi.hoisted(() => vi.fn());
vi.mock('@kb-labs/sdk', () => ({ useEnv: (name: string) => process.env[name] }));
vi.mock('@kb-labs/cli-runtime/gateway', () => ({ gatewayApiFetch }));

import { WorkflowDaemonClient } from '../http-client.js';

describe('WorkflowDaemonClient shared gateway client', () => {
  afterEach(() => { gatewayApiFetch.mockReset(); });

  // case: S-041.7, S-041.8
  it('delegates workflow REST calls to the shared authenticated client', async () => {
    gatewayApiFetch.mockResolvedValue(new Response(
      JSON.stringify({ ok: true, service: 'workflow' }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ));

    await new WorkflowDaemonClient({ projectId: 'prj_0123456789abcdef' }).health();

    expect(gatewayApiFetch).toHaveBeenCalledWith(
      'http://127.0.0.1:4000/api/v1/projects/prj_0123456789abcdef/workflow/health',
      undefined,
    );
  });
});
