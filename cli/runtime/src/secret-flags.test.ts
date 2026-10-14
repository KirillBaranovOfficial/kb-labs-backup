import { describe, expect, it } from 'vitest';
import { createContextLogger, type ILogger } from '@kb-labs/core-platform';
import { registerSecretFlagValues } from './secret-flags.js';

describe('secret-typed CLI inputs', () => {
  // case: S-203.3
  it('redacts parsed secret flags from one child log record and the returned error value', () => {
    const secret = 'x46-cli-secret-fixture';
    registerSecretFlagValues([
      { name: 'client-secret', secret: true },
      { name: 'label' },
    ], { 'client-secret': secret, label: 'ordinary value' });
    const records: Array<Record<string, unknown>> = [];
    const base: ILogger = {
      trace: () => {}, debug: () => {}, info: () => {}, warn: () => {},
      error: (message, error, fields) => records.push({ message, error, ...fields }),
      fatal: () => {}, child: () => base,
    };
    const logger = createContextLogger(base, {
      applicationId: 'kb', serviceId: 'kb', instanceId: 'test', layer: 'cli',
    }).forOperation('cli.command').forComponent('plugin');
    const thrown = new Error(`rejected ${secret}`, { cause: { credential: secret } });
    const returned = logger.redactSensitiveValues({ message: thrown.message, cause: thrown.cause });
    logger.error('handler failed', thrown);

    expect(records).toHaveLength(1);
    expect(JSON.stringify(records[0])).not.toContain(secret);
    expect(JSON.stringify(returned)).not.toContain(secret);
    expect((records[0]?.error as Error).message).toContain('[REDACTED]');
    expect(((records[0]?.error as Error).cause as { credential: string }).credential).toBe('[REDACTED]');
  });
});
