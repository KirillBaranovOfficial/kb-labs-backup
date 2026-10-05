import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { PluginContextV3 } from '@kb-labs/sdk';
import { createSessionHandler } from '../../src/rest/handlers/sessions-handler.js';

const { createSession } = vi.hoisted(() => ({ createSession: vi.fn() }));

vi.mock('@kb-labs/agent-core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@kb-labs/agent-core')>()),
  SessionManager: class {
    createSession = createSession;
  },
}));

describe('Agents session handler', () => {
  beforeEach(() => createSession.mockReset());

  // case: S-141.5
  it('rejects an unknown registered agent ID with a typed 404 before creating a session', async () => {
    const ctx = {
      cwd: '/fixture/project',
      platform: { logger: { info: vi.fn() } },
    } as unknown as PluginContextV3;

    await expect(createSessionHandler.execute(ctx, {
      body: { agentId: 'agent-does-not-exist', task: 'Summarize the project' },
    })).rejects.toMatchObject({
      statusCode: 404,
      errorCode: 'KB_AGENT_NOT_FOUND',
      message: expect.stringContaining('agent-does-not-exist'),
    });
    expect(createSession).not.toHaveBeenCalled();
  });
});
