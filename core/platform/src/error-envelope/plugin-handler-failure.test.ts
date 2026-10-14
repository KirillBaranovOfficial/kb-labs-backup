import { describe, expect, it } from 'vitest';
import type { ILogger } from '../adapters/logger.js';
import { createErrorEnvelope } from './catalog.js';
import { ErrorEnvelopeError } from './types.js';
import { createProductErrorEnvelope } from './from-error.js';
import { createContextLogger } from '../logging/context.js';
import { normalizePluginHandlerFailure } from './plugin-handler-failure.js';

function loggerFor(records: Array<Record<string, unknown>>): ILogger {
  const write = (level: string, message: string, error?: Error, fields?: Record<string, unknown>) => {
    records.push({ level, message, ...(error ? { error } : {}), ...(fields ?? {}) });
  };
  return createContextLogger({
    trace: () => {}, debug: () => {}, info: () => {}, warn: () => {},
    error: (message, error, fields) => write('error', message, error, fields),
    fatal: (message, error, fields) => write('fatal', message, error, fields),
    child: () => loggerFor(records),
  }, logContext);
}

const logContext = {
  applicationId: 'cli',
  serviceId: 'plugin-execution',
  instanceId: 'test-instance',
  layer: 'runtime',
};

describe('normalizePluginHandlerFailure', () => {
  it.each([
    new ErrorEnvelopeError(createProductErrorEnvelope({
      code: 'MIND_NOT_FOUND', retryable: false, message: 'Mind result was not found.',
      hint: 'Choose an existing mind.', details: { id: 'missing' },
    })),
    new ErrorEnvelopeError(createErrorEnvelope('KB_ADAPTER_NOT_CONFIGURED', {
      details: { service: 'llm', adapter: 'platform.adapters.llm', command: 'kb adapter bind llm' },
    })),
  ])('passes a valid typed product or platform envelope through unchanged', (cause) => {
    const records: Array<Record<string, unknown>> = [];
    const expected = cause instanceof ErrorEnvelopeError ? cause.envelope : undefined;
    const envelope = normalizePluginHandlerFailure(cause, {
      pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'typed-correlation',
      logger: loggerFor(records), logContext, sensitiveValues: [],
    });
    expect(envelope).toEqual({ ...expected, correlationId: 'typed-correlation' });
    expect(records).toHaveLength(0);
  });

  it('returns a plugin-named contract violation for a product error missing its hint', () => {
    const records: Array<Record<string, unknown>> = [];
    const envelope = normalizePluginHandlerFailure({ code: 'MIND_NOT_FOUND', message: 'Missing', retryable: false }, {
      pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'malformed-correlation',
      logger: loggerFor(records), logContext, sensitiveValues: [],
    });
    expect(envelope).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION', correlationId: 'malformed-correlation',
      message: expect.stringContaining('@kb-labs/test-plugin'), details: { field: 'hint' },
    });
    expect(records).toHaveLength(1);
  });

  it('redacts configured secret values from the one correlated exception record', () => {
    const records: Array<Record<string, unknown>> = [];
    const secret = 'x46-redaction-fixture-value';
    const envelope = normalizePluginHandlerFailure(new Error(`provider rejected ${secret}`), {
      pluginId: '@kb-labs/test-plugin',
      command: 'test run',
      correlationId: 'failure-correlation-1',
      logger: loggerFor(records),
      logContext,
      sensitiveValues: [secret],
    });

    expect(envelope.code).toBe('KB_PLUGIN_COMMAND_FAILED');
    expect(envelope.correlationId).toBe('failure-correlation-1');
    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({
      event: 'plugin.handler.failed',
      correlationId: 'failure-correlation-1',
      code: 'KB_PLUGIN_COMMAND_FAILED',
    });
    const serialized = JSON.stringify(records[0]);
    expect(serialized).not.toContain(secret);
    expect((records[0]!.error as Error).message).toContain('[REDACTED]');
  });

  it.each([
    [Object.assign(new Error('timeout'), { name: 'TimeoutError', code: 'TIMEOUT' }), 'timed out'],
    [Object.assign(new Error('worker crash'), { name: 'WorkerCrashedError', code: 'WORKER_CRASHED' }), 'execution process crashed'],
  ])('uses the shared failure code and names the reason', (error, reason) => {
    const records: Array<Record<string, unknown>> = [];
    const envelope = normalizePluginHandlerFailure(error, {
      pluginId: '@kb-labs/test-plugin',
      command: 'test run',
      correlationId: 'failure-correlation-2',
      logger: loggerFor(records),
      logContext,
      sensitiveValues: [],
    });
    expect(envelope.code).toBe('KB_PLUGIN_COMMAND_FAILED');
    expect(envelope.message).toContain(reason);
    expect(records).toHaveLength(1);
  });
});
