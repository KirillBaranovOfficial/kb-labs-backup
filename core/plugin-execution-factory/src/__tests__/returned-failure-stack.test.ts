import { describe, expect, it } from 'vitest';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { ILogger } from '@kb-labs/core-platform';
import type { PlatformServices } from '@kb-labs/plugin-contracts';
import { createExecutionBackend } from '../factory.js';
import type { ExecutionRequest } from '../types.js';

interface StructuredLogRecord {
  level: string;
  message: string;
  error?: Error;
  [field: string]: unknown;
}

const returnedStack = 'Error: serialized returned failure\n    at registeredHandler (handler.js:7:3)';

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

describe('createExecutionBackend returned failure stack diagnostics', () => {
  // case: S-203.6
  it('keeps serialized returned stack in the correlated structured log only', async () => {
    const logger = new MemoryLoggerSink();
    const platform = { logger, getAdapter: () => undefined } as unknown as PlatformServices;
    const fixturePath = fileURLToPath(new URL('./fixtures/returned-serialized-failure-handler.js', import.meta.url));
    const request: ExecutionRequest = {
      executionId: 'returned-stack-execution',
      handlerRef: './returned-serialized-failure-handler.js',
      pluginRoot: path.dirname(fixturePath),
      input: {},
      descriptor: {
        hostType: 'cli',
        pluginId: '@kb-labs/returned-failure-fixture',
        pluginVersion: '1.0.0',
        requestId: 'returned-stack-correlation',
        handlerId: 'fixture:failure',
        permissions: {},
        hostContext: { host: 'cli', argv: [], flags: {} },
      } as ExecutionRequest['descriptor'],
      context: { commandPath: 'fixture failure' },
    };
    const backend = createExecutionBackend({ platform, mode: 'in-process' });

    const result = await backend.execute(request);

    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.error.envelope).not.toHaveProperty('stack');
    expect(result.error.envelope).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION',
      correlationId: 'returned-stack-correlation',
    });
    const records = logger.queryByCorrelationId('returned-stack-correlation');
    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({
      event: 'plugin.handler.failed',
      correlationId: 'returned-stack-correlation',
      error: { stack: returnedStack },
    });
  });
});
