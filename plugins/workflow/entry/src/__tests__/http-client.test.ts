import { afterEach, describe, expect, it, vi } from 'vitest';
import { deriveProjectId } from '@kb-labs/core-project-registry';

vi.mock('@kb-labs/sdk', () => ({
  useEnv: (name: string) => process.env[name],
}));

import { getWorkflowDaemonUrl } from '../http-client.js';

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

  it('derives the current project route from cwd when no project id is injected', () => {
    delete process.env.KB_PROJECT_ID;

    expect(getWorkflowDaemonUrl()).toBe(
      `http://127.0.0.1:4000/api/v1/projects/${deriveProjectId(process.cwd())}/workflow`,
    );
  });
});
