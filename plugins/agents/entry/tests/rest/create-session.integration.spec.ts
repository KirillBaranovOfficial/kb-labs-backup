import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { PluginContextV3 } from '@kb-labs/sdk';
import { createSessionHandler } from '../../src/rest/handlers/sessions-handler.js';

let projectRoot: string | undefined;

afterEach(async () => {
  if (projectRoot) {
    await rm(projectRoot, { recursive: true, force: true });
    projectRoot = undefined;
  }
});

describe('Agents session creation REST handler', () => {
  it('creates a registered agent session in an isolated project root', async () => {
    projectRoot = await mkdtemp(join(tmpdir(), 'agents-session-'));
    const ctx = {
      cwd: projectRoot,
      platform: { logger: { info: () => undefined } },
    } as unknown as PluginContextV3;

    const response = await createSessionHandler.execute(ctx, {
      body: { agentId: 'orchestrator', task: 'Summarize the project' },
    });

    expect(response.session).toMatchObject({
      agentId: 'orchestrator',
      task: 'Summarize the project',
      status: 'active',
    });
    expect(response.session.id).not.toBe('');
    expect(JSON.parse(await readFile(
      join(projectRoot, '.kb', 'agents', 'sessions', response.session.id, 'session.json'),
      'utf8',
    ))).toMatchObject({
      id: response.session.id,
      agentId: 'orchestrator',
      task: 'Summarize the project',
    });
  });
});
