import { describe, expect, it } from 'vitest';
import {
  createContextLogger,
  createErrorEnvelope,
  createProductErrorEnvelope,
  ErrorEnvelopeError,
  ErrorEnvelopeJsonSchema,
  hasErrorEnvelopeIdentity,
  validateErrorEnvelope,
  type IContextLogger,
} from '@kb-labs/core-platform';
import { normalizePluginHandlerFailure } from '../plugin-handler-failure.js';

function loggerFor(records: Array<Record<string, unknown>>): IContextLogger {
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
      logger: loggerFor(records), logContext,
    });
    expect(envelope).toEqual({ ...expected, correlationId: 'typed-correlation' });
    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({
      code: expected?.code,
      correlationId: 'typed-correlation',
      event: 'plugin.handler.failed',
    });
  });

  it('returns a plugin-named contract violation for a product error missing its hint', () => {
    const records: Array<Record<string, unknown>> = [];
    const envelope = normalizePluginHandlerFailure({ code: 'MIND_NOT_FOUND', message: 'Missing' }, {
      pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'malformed-correlation',
      logger: loggerFor(records), logContext,
    });
    expect(envelope).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION', correlationId: 'malformed-correlation',
      message: expect.stringContaining('@kb-labs/test-plugin'), details: { field: 'hint' },
    });
    expect(records).toHaveLength(1);
  });

  it('reports the invalid field from the canonical envelope schema', () => {
    const fields = Object.keys(ErrorEnvelopeJsonSchema.properties);
    const base = createProductErrorEnvelope({
      code: 'MIND_INVALID', retryable: false, message: 'Invalid result.', hint: 'Correct the result.',
    });
    for (const field of fields) {
      const issues = validateErrorEnvelope({ ...base, [field]: null });
      expect(issues[0]?.field, field).toBe(field);
    }
    expect(validateErrorEnvelope({ ...base, unexpected: true })[0]).toMatchObject({
      field: 'unexpected',
    });
  });

  it('names every invalid envelope field derived from the canonical schema', () => {
      const records: Array<Record<string, unknown>> = [];
      const base = createProductErrorEnvelope({
        code: 'MIND_INVALID', retryable: false, message: 'Invalid result.', hint: 'Correct the result.',
      });
      const fields = Object.keys(ErrorEnvelopeJsonSchema.properties);
      const malformedByField: Array<{ field: string; malformed: Record<string, unknown> }> = fields.map((field) => ({
        field,
        malformed: { ...base, [field]: null },
      }));
      malformedByField.push({ field: 'unexpected', malformed: { ...base, unexpected: true } });
      for (const { field, malformed } of malformedByField) {
        expect(hasErrorEnvelopeIdentity(malformed), `identity for ${field}`).toBe(true);
        records.length = 0;
        const invalidField = validateErrorEnvelope(malformed)[0]?.field;
        expect(invalidField, field).toBe(field);
        const envelope = normalizePluginHandlerFailure(malformed, {
          pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'bad-field',
          logger: loggerFor(records), logContext,
        });
        expect(envelope).toMatchObject({
          code: 'KB_PLUGIN_CONTRACT_VIOLATION',
          message: expect.stringContaining(`field "${field}"`),
          details: { plugin: '@kb-labs/test-plugin', command: 'test run', field },
        });
        expect(records).toHaveLength(1);
      }
  });

  it('redacts configured secret values from the one correlated exception record', () => {
    const records: Array<Record<string, unknown>> = [];
    const secret = 'x46-redaction-fixture-value';
    const cause = new Error(`provider rejected ${secret}`, { cause: { token: secret } });
    const logger = loggerFor(records);
    logger.registerSensitiveValues([secret]);
    const envelope = normalizePluginHandlerFailure(cause, {
      pluginId: '@kb-labs/test-plugin',
      command: 'test run',
      correlationId: 'failure-correlation-1',
      logger,
      logContext,
    });

    expect(envelope.code).toBe('KB_PLUGIN_COMMAND_FAILED');
    expect(envelope.correlationId).toBe('failure-correlation-1');
    expect(records).toHaveLength(1);
    expect(JSON.stringify(envelope)).not.toContain(secret);
    expect(records[0]).toMatchObject({
      event: 'plugin.handler.failed',
      correlationId: 'failure-correlation-1',
      code: 'KB_PLUGIN_COMMAND_FAILED',
    });
    const serialized = JSON.stringify(records[0]);
    expect(serialized).not.toContain(secret);
    expect((records[0]!.error as Error).message).toContain('[REDACTED]');
    expect(((records[0]!.error as Error).cause as { token: string }).token).toBe('[REDACTED]');
  });

  it('redacts process secrets from a typed envelope returned to the caller', () => {
    const records: Array<Record<string, unknown>> = [];
    const secret = 'x46-typed-envelope-secret-fixture';
    const logger = loggerFor(records);
    logger.registerSensitiveValues([secret]);
    const envelope = normalizePluginHandlerFailure(new ErrorEnvelopeError(createProductErrorEnvelope({
      code: 'MIND_NOT_FOUND', retryable: false,
      message: `Provider rejected ${secret}`,
      hint: 'Choose another mind.', details: { credential: secret },
    })), {
      pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'typed-secret-correlation',
      logger, logContext,
    });

    expect(records).toHaveLength(1);
    expect(JSON.stringify(envelope)).not.toContain(secret);
    expect(envelope.message).toContain('[REDACTED]');
    expect(envelope.details?.credential).toBe('[REDACTED]');
  });

  it('keeps the original thrown stack on its single correlated log record', () => {
    const records: Array<Record<string, unknown>> = [];
    const cause = new Error('handler diagnostic');
    cause.stack = 'original-handler-stack';
    normalizePluginHandlerFailure(cause, {
      pluginId: '@kb-labs/test-plugin', command: 'test run', correlationId: 'stack-correlation',
      logger: loggerFor(records), logContext,
    });
    expect(records).toHaveLength(1);
    expect((records[0]!.error as Error).stack).toBe('original-handler-stack');
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
    });
    expect(envelope.code).toBe('KB_PLUGIN_COMMAND_FAILED');
    expect(envelope.message).toContain(reason);
    expect(records).toHaveLength(1);
  });
});
