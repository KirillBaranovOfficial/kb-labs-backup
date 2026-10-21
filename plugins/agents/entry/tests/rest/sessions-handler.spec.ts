import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { PluginContextV3 } from '@kb-labs/sdk';
import { AGENTS_ROUTES } from '@kb-labs/agent-contracts';
import { manifest } from '../../src/manifest.js';
import { createSessionHandler } from '../../src/rest/handlers/sessions-handler.js';
import getSessionHandler from '../../src/rest/handlers/get-session-handler.js';

const { createSession, getSessionInfo } = vi.hoisted(() => ({
  createSession: vi.fn(),
  getSessionInfo: vi.fn(),
}));

vi.mock('@kb-labs/agent-core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@kb-labs/agent-core')>()),
  SessionManager: class {
    createSession = createSession;
    getSessionInfo = getSessionInfo;
  },
}));

describe('Agents session handler', () => {
  beforeEach(() => {
    createSession.mockReset();
    getSessionInfo.mockReset();
  });

  it('registers the validated session creation handler for POST /sessions', () => {
    const route = manifest.rest.routes.find((candidate) => candidate.method === 'POST'
      && candidate.path === AGENTS_ROUTES.SESSION_CREATE);

    expect(route?.handler).toBe('./rest/handlers/sessions-handler.js#createSessionHandler');
  });

  it('creates a session for a registered agent', async () => {
    const session = {
      id: 'session-1',
      agentId: 'orchestrator',
      task: 'Summarize the project',
      status: 'active',
    };
    createSession.mockResolvedValue(session);
    const ctx = {
      cwd: '/fixture/project',
      platform: { logger: { info: vi.fn() } },
    } as unknown as PluginContextV3;

    await expect(createSessionHandler.execute(ctx, {
      body: { agentId: 'orchestrator', task: 'Summarize the project' },
    })).resolves.toEqual({ session });
    expect(createSession).toHaveBeenCalledWith({
      mode: 'execute',
      task: 'Summarize the project',
      agentId: 'orchestrator',
      name: undefined,
    });
  });

  // case: S-141.5
  it('rejects an unknown agent ID with a typed 404 before creating a session', async () => {
    const ctx = {
      cwd: '/fixture/project',
      platform: { logger: { info: vi.fn() } },
    } as unknown as PluginContextV3;

    await expect(createSessionHandler.execute(ctx, {
      body: { agentId: 'agent-does-not-exist', task: 'Summarize the project' },
    })).rejects.toMatchObject({
      code: 'KB_AGENT_NOT_FOUND',
      statusCode: 404,
      details: { agentId: 'agent-does-not-exist' },
      message: expect.stringContaining('agent-does-not-exist'),
    });
    expect(createSession).not.toHaveBeenCalled();
  });

  // case: S-141.4
  it('returns a typed not-found error for an unknown session', async () => {
    getSessionInfo.mockResolvedValue(null);
    const ctx = { cwd: '/fixture/project' } as PluginContextV3;

    await expect(getSessionHandler.execute(ctx, {
      params: { sessionId: 'session-does-not-exist' },
    })).rejects.toMatchObject({
      code: 'KB_RUNTIME_RESOURCE_NOT_FOUND',
      statusCode: 404,
      details: { plugin: '@kb-labs/agents', sessionId: 'session-does-not-exist' },
      message: expect.stringContaining('session-does-not-exist'),
    });
  });
});
