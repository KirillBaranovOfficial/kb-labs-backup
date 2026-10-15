import { describe, expect, it } from 'vitest';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateErrorEnvelope, type ILogger } from '@kb-labs/core-platform';
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
const productStack = 'PluginError: typed plugin failure\n    at registeredHandler (bundled-plugin.js:31:5)';
const platformStack = 'AdapterUnavailableError: fixture adapter unavailable\n    at registeredHandler (adapter-plugin.js:19:7)';
const backends = ['in-process', 'worker-pool', 'subprocess'] as const;
interface FailureCase {
  label: string;
  handler: string;
  expectedCode: string;
  name: string;
  message: string;
  stack?: string;
  stackIncludes?: string;
}

const failures: FailureCase[] = [
  {
    label: 'returned serialized failure',
    handler: 'returned-serialized-failure-handler.js',
    expectedCode: 'KB_PLUGIN_CONTRACT_VIOLATION',
    name: 'Error',
    message: 'returned handler failure',
    stack: returnedStack,
  },
  {
    label: 'plain thrown Error',
    handler: 'thrown-failure-handler.js',
    expectedCode: 'KB_PLUGIN_COMMAND_FAILED',
    name: 'Error',
    message: thrownMessage,
    stackIncludes: thrownMessage,
  },
  {
    label: 'typed plugin error with a catalogued code',
    handler: 'typed-product-failure-handler.js',
    expectedCode: 'KB_PLUGIN_PERMISSION_DENIED',
    name: 'PluginError',
    message: 'typed plugin failure',
    stack: productStack,
  },
  {
    label: 'catalogued platform adapter error',
    handler: 'platform-adapter-failure-handler.js',
    expectedCode: 'KB_ADAPTER_NOT_CONFIGURED',
    name: 'AdapterUnavailableError',
    message: 'fixture adapter unavailable',
    stack: platformStack,
  },
];

type FailureFixture = FailureCase['handler'];

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

function makeRequest(handler: FailureFixture, correlationId: string): ExecutionRequest {
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
  it.each(failures)('validates the $label envelope and retains its stack in the correlated log', async (failure) => {
    const logger = new MemoryLoggerSink();
    const platform = { logger, getAdapter: () => undefined } as unknown as PlatformServices;
    const backend = makeBackend(mode, platform, logger);
    const correlationId = `${mode}-${failure.handler}`;

    try {
      const result = await backend.execute(makeRequest(failure.handler, correlationId));

      expect(result.ok).toBe(false);
      if (result.ok) return;
      expect(result.error.code, `${mode} ${failure.handler} returned ${result.error.error.name}: ${result.error.error.message}`).toBe(failure.expectedCode);
      expect(result.error.envelope.code).toBe(failure.expectedCode);
      expect(result.error).not.toHaveProperty('stack');
      expect(result.error.envelope).not.toHaveProperty('stack');
      expect(validateErrorEnvelope(result.error.envelope)).toEqual([]);

      const [record] = logger.queryByCorrelationId(correlationId);
      expect(record).toEqual(expect.objectContaining({
        event: 'plugin.handler.failed',
        correlationId,
        serializedError: expect.objectContaining({
          name: failure.name,
          message: failure.message,
          ...(failure.stack ? { stack: failure.stack } : {}),
          ...(failure.stackIncludes ? { stack: expect.stringContaining(failure.stackIncludes) } : {}),
        }),
      }));
      expect((record?.serializedError as { stack?: string } | undefined)?.stack).toContain(
        failure.stackIncludes ?? failure.stack,
      );
    } finally {
      await backend.shutdown();
    }
  });

  // case: S-203.6
  it('turns a returned malformed hint into a valid platform envelope', async () => {
    const logger = new MemoryLoggerSink();
    const platform = { logger, getAdapter: () => undefined } as unknown as PlatformServices;
    const backend = makeBackend(mode, platform, logger);
    const correlationId = `${mode}-returned-invalid-envelope`;

    try {
      const result = await backend.execute(makeRequest('returned-invalid-envelope-handler.js', correlationId));

      expect(result.ok).toBe(false);
      if (result.ok) return;
      expect(result.error.envelope).toMatchObject({
        code: 'KB_PLUGIN_CONTRACT_VIOLATION',
        correlationId,
        details: { field: 'hint' },
      });
      expect(validateErrorEnvelope(result.error.envelope)).toEqual([]);
      expect(logger.queryByCorrelationId(correlationId)).toHaveLength(1);
    } finally {
      await backend.shutdown();
    }
  });
});
