import { describe, expect, it, vi } from 'vitest';
import type { ILogger } from '@kb-labs/core-platform';
import { createProductErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';
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
  const failures = [
    ['timeout', Object.assign(new Error('handler timeout'), { name: 'TimeoutError', code: 'TIMEOUT' }), 'KB_PLUGIN_COMMAND_FAILED'],
    ['crash', Object.assign(new Error('worker crash'), { name: 'WorkerCrashedError', code: 'WORKER_CRASHED' }), 'KB_PLUGIN_COMMAND_FAILED'],
    ['plain throw', new Error('handler exploded'), 'KB_PLUGIN_COMMAND_FAILED'],
    ['typed product error', new ErrorEnvelopeError(createProductErrorEnvelope({
      code: 'MIND_NOT_FOUND', retryable: false, message: 'Mind result not found.', hint: 'Choose a known mind.',
    })), 'MIND_NOT_FOUND'],
    ['malformed product envelope', { code: 'MIND_NOT_FOUND', message: 'Mind result not found.', retryable: false }, 'KB_PLUGIN_CONTRACT_VIOLATION'],
  ] as const;

  const returnedFailures = [
    ['typed product result', {
      ok: false,
      error: createProductErrorEnvelope({
        code: 'MIND_NOT_FOUND', retryable: false, message: 'Mind result not found.', hint: 'Choose a known mind.',
      }),
    }, 'MIND_NOT_FOUND'],
    ['malformed product result', {
      ok: false,
      error: { code: 'MIND_NOT_FOUND', message: 'Mind result not found.' },
    }, 'KB_PLUGIN_CONTRACT_VIOLATION'],
    ['malformed command failure claim', { ok: false }, 'KB_PLUGIN_CONTRACT_VIOLATION'],
  ] as const;

  it.each(['in-process', 'subprocess', 'worker-pool', 'remote'] as const)(
    'normalizes each failure kind once in %s mode',
    async (mode) => {
      for (const [failureName, cause, expectedCode] of failures) {
        runBackend.mockReset();
        logError.mockClear();
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

        expect(result.ok, `${mode} / ${failureName}`).toBe(false);
        if (result.ok) continue;
        expect(result.error.envelope.code, `${mode} / ${failureName}: ${JSON.stringify(result.error.envelope)}`).toBe(expectedCode);
        expect(result.error.envelope.correlationId, `${mode} / ${failureName}`).toBe('correlation-1');
        expect(logError, `${mode} / ${failureName}`).toHaveBeenCalledOnce();
        const loggedError = logError.mock.calls[0]?.[1];
        if (cause instanceof Error) expect(loggedError?.stack).toBe(cause.stack);
      }
    },
  );

  it.each(['in-process', 'subprocess', 'worker-pool', 'remote'] as const)(
    'validates returned failure envelopes at the boundary in %s mode',
    async (mode) => {
      for (const [failureName, data, expectedCode] of returnedFailures) {
        runBackend.mockReset();
        logError.mockClear();
        runBackend.mockResolvedValueOnce({ ok: true, data, executionTimeMs: 1 });
        const backend = createExecutionBackend({
          platform: { logger } as unknown as Parameters<typeof createExecutionBackend>[0]['platform'],
          mode,
          remote: mode === 'remote' ? { transport: { execute: vi.fn() } } : undefined,
        });
        const result = await backend.execute({
          ...request,
          target: mode === 'remote' ? { environmentId: 'env-1' } : undefined,
        });
        expect(result.ok, `${mode} / ${failureName}`).toBe(false);
        if (result.ok) continue;
        expect(result.error.envelope.code, `${mode} / ${failureName}: ${JSON.stringify(result.error.envelope)}`).toBe(expectedCode);
        expect(result.error.envelope.correlationId, `${mode} / ${failureName}`).toBe('correlation-1');
        expect(logError, `${mode} / ${failureName}`).toHaveBeenCalledOnce();
      }
    },
  );

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
        normalized: true,
        envelope: {
          code: 'KB_PLUGIN_COMMAND_FAILED',
          correlationId: 'correlation-1',
          details: {
            plugin: '@kb-labs/test-plugin',
            command: 'test run',
            failureReason: 'failed unexpectedly',
          },
        },
      });
      expect(logError).toHaveBeenCalledOnce();
    },
  );
});
