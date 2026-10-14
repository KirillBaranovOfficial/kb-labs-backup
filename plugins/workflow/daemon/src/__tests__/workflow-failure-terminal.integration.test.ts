import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createRegistry } from '@kb-labs/core-registry';
import { FaultInjectingCache, InMemoryCache, InMemoryEventBus, InMemoryStorage } from '@kb-labs/core-platform/inmemory';
import { mockLogger } from '@kb-labs/shared-testing';
import { EnvSecretProvider, WorkflowEngine, workflowDeadLetterCacheKey, workflowProjectCacheKey } from '@kb-labs/workflow-engine';
import { createNormalizedExecutionBackend } from '@kb-labs/plugin-execution';
import { createWorkflowWorker } from '../worker.js';

// case: S-029.2, S-009.4
describe('live workflow failure terminalization', () => {
  let roots: string[] = [];

  afterEach(async () => {
    await Promise.all(roots.map((root) => rm(root, { recursive: true, force: true })));
    roots = [];
  });

  it.each([
    ['handler throws', 'builtin:shell', { command: 'throw-handler' }, 'failed unexpectedly'],
    ['plugin is missing', 'plugin:@kb-labs/missing/handle', {}, 'Plugin not found'],
    ['command is unknown', 'command:missing:command', {}, 'CLI command not found'],
    ['shell exits non-zero', 'builtin:shell', { command: 'exit 23' }, 'shell exited 23'],
  ])('%s reaches failed run and persisted history without restart', async (_kind, uses, input, diagnostic) => {
    const workspaceRoot = await mkdtemp(join(tmpdir(), 'workflow-live-failure-'));
    roots.push(workspaceRoot);
    const logger = mockLogger();
    let deadLetterKey: string | undefined;
    const projectId = 'prj_0000000000000001';
    const cache = new FaultInjectingCache(new InMemoryCache(), (operation) =>
      operation.name === 'set' && operation.key === deadLetterKey
        ? new Error('dead-letter cache unavailable')
        : undefined,
    );
    const storage = new InMemoryStorage();
    const events = new InMemoryEventBus();
    const engine = new WorkflowEngine({
      projectId, cache, storage, events, logger,
      secretProvider: new EnvSecretProvider(), workspaceRoot,
    });
    const run = await engine.createRun({
      spec: {
        name: 'terminal-failure', version: '1.0.0', on: { manual: true },
        jobs: { failure: { runsOn: 'local', steps: [{ name: 'Fail', uses, with: input }] } },
      },
      trigger: { type: 'manual' }, metadata: { workflowId: 'terminal-failure' },
    });
    const failedJobId = run.jobs[0]!.id;
    deadLetterKey = workflowProjectCacheKey(projectId, workflowDeadLetterCacheKey(run.id, failedJobId));
    const registry = await createRegistry({ root: workspaceRoot });
    let executionCount = 0;
    const backend = createNormalizedExecutionBackend({
      async execute(request) {
        executionCount++;
        if (request.input && typeof request.input === 'object' && 'command' in request.input
            && request.input.command === 'exit 23') {
          return { ok: true as const, data: { exitCode: 23, stderr: 'shell exited 23' }, executionTimeMs: 1 };
        }
        throw new Error('handler threw');
      },
      async health() { return { healthy: true, backend: 'in-process' }; },
      async stats() { return { totalExecutions: executionCount, successCount: 0, errorCount: executionCount, avgExecutionTimeMs: 1 }; },
      async shutdown() {},
    }, logger);
    const worker = await createWorkflowWorker({
      engine, secretProvider: new EnvSecretProvider(), cliApi: registry,
      logger,
      platform: { executionBackend: backend, hasExecutionBackend: true, getAdapter: () => undefined },
      workspaceRoot, projectRoot: workspaceRoot, projectId: 'prj_0000000000000001', concurrency: 1,
    });

    const workerRun = worker.start();
    try {
      await vi.waitFor(async () => {
        const current = await engine.getRun(run.id);
        expect(current?.status).toBe('failed');
      }, { timeout: 3000 });
      const current = await engine.getRun(run.id);
      expect(current?.jobs[0]).toMatchObject({
        status: 'failed', error: { message: expect.stringContaining(diagnostic) },
        steps: [{ status: 'failed', error: { message: expect.stringContaining(diagnostic) } }],
      });
      expect(current?.result).toMatchObject({ status: 'failed', error: { message: expect.stringContaining(diagnostic) } });
      await expect(engine.listRuns()).resolves.toEqual([expect.objectContaining({ id: run.id, status: 'failed' })]);
    } finally {
      await worker.stop();
      await workerRun;
      await registry.dispose();
    }
  });
});
