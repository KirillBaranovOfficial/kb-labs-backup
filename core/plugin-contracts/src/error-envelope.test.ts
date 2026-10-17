import { describe, expect, it } from 'vitest';
import {
  createProductErrorEnvelope,
  toErrorEnvelope,
  validateErrorEnvelope,
} from '@kb-labs/core-platform';
import type { PlatformErrorCode } from '@kb-labs/core-platform/error-envelope';
import {
  AbortError,
  ConfigError,
  PermissionError,
  PlatformError,
  PluginError,
  ProductPluginError,
  RateLimitError,
  TimeoutError,
  ValidationError,
} from './errors.js';
describe('core toErrorEnvelope for plugin errors', () => {
  const cases: Array<[string, PluginError<PlatformErrorCode>, string]> = [
    ['validation', new ValidationError('bad', { field: 'cwd' }), 'KB_RUNTIME_INPUT_INVALID'],
    ['config', new ConfigError('bad config', { path: 'llm.model' }), 'KB_CONFIG_INVALID'],
    ['timeout', new TimeoutError('slow'), 'KB_RUNTIME_TIMEOUT'],
    ['permission', new PermissionError('no', { plugin: 'commit', permission: 'fs.write' }), 'KB_PLUGIN_PERMISSION_DENIED'],
    ['rate limit', new RateLimitError('slow down', 2500), 'KB_RUNTIME_RATE_LIMITED'],
    ['platform', new PlatformError('llm', 'down'), 'KB_HOST_ADAPTER_UNAVAILABLE'],
    ['abort', new AbortError(), 'KB_PLUGIN_ABORTED'],
    ['platform code without a product hint', new PluginError('boom', 'KB_RUNTIME_COMMAND_FAILED'), 'KB_RUNTIME_COMMAND_FAILED'],
  ];

  for (const [name, error, code] of cases) {
    it(`maps ${name} to ${code} as a valid envelope`, () => {
      const envelope = toErrorEnvelope(error, { correlationId: 'c1', binary: 'test-cli' });
      expect(validateErrorEnvelope(envelope)).toEqual([]);
      expect(envelope.code).toBe(code);
      expect(envelope.correlationId).toBe('c1');
      expect(envelope.message).not.toMatch(/\{[^}]+\}/);
      expect(envelope.hint).not.toMatch(/\{[^}]+\}/);
    });
  }

  it('renders details into catalog templates', () => {
    expect(toErrorEnvelope(new ConfigError('Invalid config.', { path: 'llm.model' }))).toMatchObject({
      code: 'KB_CONFIG_INVALID', message: 'Invalid config.', details: { path: 'llm.model' },
    });
    expect(toErrorEnvelope(new RateLimitError('Rate limit reached.', 2500))).toMatchObject({
      code: 'KB_RUNTIME_RATE_LIMITED', message: 'Rate limit reached.', details: { retryAfter: '3s' },
    });
  });

  it('rejects structured details rather than serializing them into strings', () => {
    const envelope = toErrorEnvelope(Object.assign(new Error('bad'), {
      code: 'KB_RUNTIME_COMMAND_FAILED',
      message: 'bad',
      hint: 'Resolve the failure, then retry.',
      details: { report: '{"findings":[]}' },
    }), { correlationId: 'c2', binary: 'test-cli' });
    expect(envelope).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION',
      correlationId: 'c2',
    });
    expect(validateErrorEnvelope(envelope)).toEqual([]);
  });

  it('delegates non-plugin errors to core-platform', () => {
    expect(toErrorEnvelope(new Error('boom')).code).toBe('KB_RUNTIME_UNEXPECTED');
  });

  it('keeps a validated product plugin code and rejects a missing hint', () => {
    const productError = createProductErrorEnvelope({
      code: 'COMMIT_NOTHING_STAGED',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
      retryable: false,
    });
    const error = new ProductPluginError(
      'Nothing is staged.',
      productError.code,
      undefined,
      'Stage the intended changes, then retry.',
    );
    expect(toErrorEnvelope(error)).toMatchObject({
      code: 'COMMIT_NOTHING_STAGED',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
      area: 'product',
    });
    expect(toErrorEnvelope(new ProductPluginError('Nothing is staged.', productError.code))).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION',
    });
  });
});
