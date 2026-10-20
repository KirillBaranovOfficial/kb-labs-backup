import { describe, expect, it } from 'vitest';
import type { ILogger } from '../adapters/logger.js';
import { createContextLogger } from './context.js';
import { logUnexpectedCliError } from './unexpected-error.js';

function captureLogger(records: Array<Record<string, unknown>>): ILogger {
  const write = (level: string, message: string, error?: Error, meta?: Record<string, unknown>) => {
    records.push({ level, message, ...(error ? { error } : {}), ...(meta ?? {}) });
  };
  return {
    trace: (message, meta) => write('trace', message, undefined, meta),
    debug: (message, meta) => write('debug', message, undefined, meta),
    info: (message, meta) => write('info', message, undefined, meta),
    warn: (message, meta) => write('warn', message, undefined, meta),
    error: (message, error, meta) => write('error', message, error, meta),
    fatal: (message, error, meta) => write('fatal', message, error, meta),
    child: () => captureLogger(records),
    flush: async () => {},
  };
}

describe('logUnexpectedCliError', () => {
  it('writes a correlated, secret-redacted error event with the thrown cause', () => {
    const records: Array<Record<string, unknown>> = [];
    const logger = createContextLogger(captureLogger(records), {
      applicationId: 'kb',
      serviceId: 'kb',
      instanceId: 'test',
      layer: 'cli',
    });
    logger.registerSensitiveValues(['sentinel-secret']);
    const error = new TypeError('failed with sentinel-secret');
    logUnexpectedCliError(logger, error, 'correlation-17');

    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({
      level: 'error',
      event: 'cli.unexpected_error',
      operation: 'cli.command.failed',
      correlationId: 'correlation-17',
      errorClass: 'TypeError',
      errorMessage: 'failed with [REDACTED]',
      causeChain: [expect.objectContaining({
        name: 'TypeError',
        message: 'failed with [REDACTED]',
        stack: expect.stringContaining('[REDACTED]'),
      })],
      stack: expect.stringContaining('[REDACTED]'),
    });
    expect(JSON.stringify(records)).not.toContain('sentinel-secret');
  });
});
