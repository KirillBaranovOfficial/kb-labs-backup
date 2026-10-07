import { describe, expect, it } from 'vitest';
import { AdapterUnavailableError, toErrorEnvelope, validateErrorEnvelope } from '@kb-labs/core-platform';
import { PluginError, defineError } from '../errors/index';
import { ServiceNotConfiguredError } from '../helpers/platform';
import { ValidationError } from '../helpers/validation';

describe('core toErrorEnvelope for command-kit errors', () => {
  it('maps a defineError PluginError to a product envelope', () => {
    const MindError = defineError('MIND', {
      IndexNotFound: { code: 404, message: (scope: string) => `Index '${scope}' not found` },
      Timeout: { code: 504, message: 'Query timed out' },
    });

    const notFound = toErrorEnvelope(new MindError.IndexNotFound('default'), { correlationId: 'c1' });
    expect(validateErrorEnvelope(notFound)).toEqual([]);
    expect(notFound).toMatchObject({
      code: 'MIND_INDEXNOTFOUND',
      area: 'product',
      severity: 'error',
      retryable: false,
      message: "Index 'default' not found",
      correlationId: 'c1',
      details: { statusCode: '404' },
    });

    const timeout = toErrorEnvelope(new MindError.Timeout());
    expect(timeout.retryable).toBe(true);
  });

  it('rejects structured PluginError details with a catalogued contract violation', () => {
    const envelope = toErrorEnvelope(
      new PluginError('weird code!', 'Nope', 400, { field: 'cwd', limits: { max: 3 } }),
    );
    expect(validateErrorEnvelope(envelope)).toEqual([]);
    expect(envelope.code).toBe('KB_PLUGIN_CONTRACT_VIOLATION');
    expect(envelope.hint).toContain('error.diagnostics');
  });

  it('uses the catalog when a PluginError carries a catalog code', () => {
    const envelope = toErrorEnvelope(new PluginError('KB_PLUGIN_NOT_FOUND', 'x', 404, { plugin: 'acme' }));
    expect(envelope.area).toBe('plugin');
    expect(envelope.message).toBe('Plugin acme was not found in the registry.');
    expect(envelope.cause).toBe('x');
  });

  it('maps ServiceNotConfiguredError to KB_ADAPTER_NOT_CONFIGURED', () => {
    const envelope = toErrorEnvelope(new ServiceNotConfiguredError('llm', '@kb-labs/adapters-openai'));
    expect(validateErrorEnvelope(envelope)).toEqual([]);
    expect(envelope.code).toBe('KB_ADAPTER_NOT_CONFIGURED');
    expect(envelope.hint).toBe(
      'Run `kb adapter bind llm @kb-labs/adapters-openai` to bind an installed adapter for this capability.',
    );

    const cacheAdapter = toErrorEnvelope(new ServiceNotConfiguredError('cache', '@kb-labs/adapters-redis'));
    expect(cacheAdapter.hint).toBe(
      'Run `kb adapter bind cache @kb-labs/adapters-redis` to bind an installed adapter for this capability.',
    );
  });

  it('maps ValidationError issues into details', () => {
    const envelope = toErrorEnvelope(
      new ValidationError('bad input', [
        { path: 'name', message: 'Required' },
        { path: '', message: 'Unexpected value' },
      ]),
    );
    expect(validateErrorEnvelope(envelope)).toEqual([]);
    expect(envelope.code).toBe('KB_RUNTIME_INPUT_INVALID');
    expect(envelope.details).toEqual({ name: 'Required', value: 'Unexpected value' });
  });

  it('delegates AdapterUnavailableError and unknown values to core-platform', () => {
    expect(toErrorEnvelope(new AdapterUnavailableError('llm')).code).toBe('KB_ADAPTER_NOT_CONFIGURED');
    expect(toErrorEnvelope(new Error('boom')).code).toBe('KB_RUNTIME_UNEXPECTED');
  });
});
