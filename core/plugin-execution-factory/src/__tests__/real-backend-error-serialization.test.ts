import { describe, expect, it } from 'vitest';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { ILogger } from '@kb-labs/core-platform';
import type { PlatformServices } from '@kb-labs/plugin-contracts';
import { createExecutionBackend, createNormalizedExecutionBackend } from '../factory.js';
import { WorkerPoolBackend } from '../backends/worker-pool/backend.js';
import type { ExecutionBackend, ExecutionRequest } from '../types.js';

interface StructuredLogRecord {
  level: string;
  message: string;
  error?: Error;
  [field: string]: unknown;
}

class MemoryLoggerSink implements ILogger {
  readonly records: StructuredLogRecord[] = [];

  trace(message: string, meta?: Record<string, unknown>) { this.write('trace', message, undefined, meta); }
  debug(message: string, meta?: Record<string, unknown>) { this.write('debug', message, undefined, meta); }
  info(message: string, meta?: Record<string, unknown>) { this.write('info', message, undefined, meta); }
  warn(message: string, meta?: Record<string, unknown>) { this.write('warn', message, undefined, meta); }
  error(message: string, error?: Error, meta?: Record<string, unknown>) { this.write('error', message, error, meta); }
  fatal(message: string, error?: Error, meta?: Record<string, unknown>) { this.write('fatal', message, error, meta); }
  child() { return this; }

  queryByCorrelationId(correlationId: string): StructuredLogRecord[] {
    return this.records.filter((record) => record.correlationId === correlationId);
  }

  private write(level: string, message: string, error?: Error, meta?: Record<string, unknown>) {
    this.records.push({ level, message, ...(meta ?? {}), ...(error ? { error } : {}) });
  }
}

const returnedStack = 'Error: serialized returned failure\n    at registeredHandler (handler.js:7:3)';
const thrownMessage = 'FIXTURE_THROW_STACK';
const backends = ['in-process', 'worker-pool', 'subprocess'] as const;

function makeBackend(mode: (typeof backends)[number], platform: PlatformServices, logger: ILogger): ExecutionBackend {
  if (mode !== 'worker-pool') {
    return createExecutionBackend({
      platform,
      mode,
      workerPool: { min: 1, max: 1 },
    });
  }

  const workerScript = fileURLToPath(new URL('../../dist/backends/worker-pool/worker-script.js', import.meta.url));
  const rawBackend = new WorkerPoolBackend({
    platform,
    min: 1,
    max: 1,
    workerScript,
  });
  return createNormalizedExecutionBackend(rawBackend, logger);
}

function makeRequest(
  handler: 'returned-serialized-failure-handler.js' | 'thrown-failure-handler.js',
  correlationId: string,
): ExecutionRequest {
  const fixturePath = fileURLToPath(new URL(`./fixtures/${handler}`, import.meta.url));
  return {
    executionId: `${correlationId}-execution`,
    handlerRef: `./${handler}`,
    pluginRoot: path.dirname(fixturePath),
    input: {},
    descriptor: {
      hostType: 'cli',
      pluginId: '@kb-labs/failure-fixture',
      pluginVersion: '1.0.0',
      requestId: correlationId,
      handlerId: 'fixture:failure',
      permissions: {},
      hostContext: { host: 'cli', argv: [], flags: {} },
    } as ExecutionRequest['descriptor'],
    context: { commandPath: 'fixture failure' },
  };
}

describe.each(backends)('%s real failure dispatch', (mode) => {
  it('retains a returned serialized failure stack only in its correlated log', async () => {
    const logger = new MemoryLoggerSink();
    const platform = { logger, getAdapter: () => undefined } as unknown as PlatformServices;
    const backend = makeBackend(mode, platform, logger);
    const correlationId = `${mode}-returned-stack`;

    try {
      const result = await backend.execute(makeRequest('returned-serialized-failure-handler.js', correlationId));

      expect(result.ok).toBe(false);
      if (result.ok) return;
      expect(result.error.envelope).not.toHaveProperty('stack');
      expect(logger.queryByCorrelationId(correlationId)).toEqual([
        expect.objectContaining({
          event: 'plugin.handler.failed',
          correlationId,
          serializedError: expect.objectContaining({
            name: 'Error',
            message: 'returned handler failure',
            stack: returnedStack,
          }),
        }),
      ]);
    } finally {
      await backend.shutdown();
    }
  });

  it('serializes a thrown handler error and logs its source stack by correlation id', async () => {
    const logger = new MemoryLoggerSink();
    const platform = { logger, getAdapter: () => undefined } as unknown as PlatformServices;
    const backend = makeBackend(mode, platform, logger);
    const correlationId = `${mode}-thrown-stack`;

    try {
      const result = await backend.execute(makeRequest('thrown-failure-handler.js', correlationId));

      expect(result.ok).toBe(false);
      if (result.ok) return;
      expect(result.error.envelope).not.toHaveProperty('stack');
      expect(logger.queryByCorrelationId(correlationId)).toEqual([
        expect.objectContaining({
          event: 'plugin.handler.failed',
          correlationId,
          serializedError: expect.objectContaining({
            name: 'Error',
            message: thrownMessage,
            stack: expect.stringContaining(thrownMessage),
          }),
        }),
      ]);
    } finally {
      await backend.shutdown();
    }
  });

});
