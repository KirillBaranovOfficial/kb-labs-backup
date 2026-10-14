import { describe, expect, it, vi } from 'vitest';
import type { ILogger } from '@kb-labs/core-platform';
import type { ExecutionRequest, ExecutionResult } from '../types.js';
import { createExecutionBackend } from '../factory.js';

const { runBackend } = vi.hoisted(() => ({
  runBackend: vi.fn<(...args: unknown[]) => Promise<ExecutionResult>>(),
}));

vi.mock('../backends/in-process.js', () => ({
  InProcessBackend: class { execute(...args: unknown[]) { return runBackend(...args); } },
}));
vi.mock('../backends/subprocess.js', () => ({
  SubprocessBackend: class { execute(...args: unknown[]) { return runBackend(...args); } },
}));
vi.mock('../backends/worker-pool/backend.js', () => ({
  WorkerPoolBackend: class { execute(...args: unknown[]) { return runBackend(...args); } },
}));
vi.mock('../backends/remote.js', () => ({
  RemoteBackend: class { execute(...args: unknown[]) { return runBackend(...args); } },
}));

const logError = vi.fn();
const logger: ILogger = {
  trace: vi.fn(),
  debug: vi.fn(),
  info: vi.fn(),
  warn: vi.fn(),
  error: (...args) => logError(...args),
  fatal: vi.fn(),
  child: () => logger,
};

const request: ExecutionRequest = {
  executionId: 'execution-1',
  handlerRef: './dist/handler.js',
  pluginRoot: '/plugin',
  input: {},
  descriptor: {
    hostType: 'workflow',
    pluginId: '@kb-labs/test-plugin',
    pluginVersion: '1.0.0',
    requestId: 'correlation-1',
  } as unknown as ExecutionRequest['descriptor'],
  context: { commandPath: 'test run' },
};

describe('createExecutionBackend handler failure boundary', () => {
  it.each(['in-process', 'subprocess', 'worker-pool', 'remote'] as const)(
    'normalises an unexpected handler failure once in %s mode',
    async (mode) => {
      logError.mockClear();
      const cause = new Error('handler exploded');
      runBackend.mockRejectedValueOnce(cause);
      const backend = createExecutionBackend({
        platform: { logger } as unknown as Parameters<typeof createExecutionBackend>[0]['platform'],
        mode,
        remote: mode === 'remote' ? { transport: { execute: vi.fn() } } : undefined,
      });

      const result = await backend.execute({
        ...request,
        target: mode === 'remote' ? { environmentId: 'env-1' } : undefined,
      });

      expect(result.ok).toBe(false);
      expect(result.error).toMatchObject({
        code: 'KB_PLUGIN_COMMAND_FAILED',
        correlationId: 'correlation-1',
        details: {
          plugin: '@kb-labs/test-plugin',
          command: 'test run',
          failureReason: 'failed unexpectedly',
        },
      });
      expect(logError).toHaveBeenCalledOnce();
    },
  );
});
