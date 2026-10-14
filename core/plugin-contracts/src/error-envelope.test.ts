import { describe, expect, it } from 'vitest';
import {
  AdapterUnavailableError,
  createProductErrorEnvelope,
  toErrorEnvelope,
  validateErrorEnvelope,
} from '@kb-labs/core-platform';
import {
  AbortError,
  ConfigError,
  PermissionError,
  PlatformError,
  PluginError,
  RateLimitError,
  TimeoutError,
  ValidationError,
  wrapError,
} from './errors.js';
describe('core toErrorEnvelope for plugin errors', () => {
  const cases: Array<[string, PluginError, string]> = [
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
      const envelope = toErrorEnvelope(error, { correlationId: 'c1' });
      expect(validateErrorEnvelope(envelope)).toEqual([]);
      expect(envelope.code).toBe(code);
      expect(envelope.correlationId).toBe('c1');
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
    const envelope = toErrorEnvelope(new PluginError('bad', 'KB_RUNTIME_COMMAND_FAILED', {
      report: '{"findings":[]}',
    }), { correlationId: 'c2' });
    expect(envelope).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION',
      correlationId: 'c2',
    });
    expect(validateErrorEnvelope(envelope)).toEqual([]);
  });

  it('delegates non-plugin errors to core-platform', () => {
    expect(toErrorEnvelope(new Error('boom')).code).toBe('KB_RUNTIME_UNEXPECTED');
  });

  it('preserves a catalogued platform code when wrapping an adapter error for subprocess IPC', () => {
    const source = new AdapterUnavailableError('llm', 'not-configured', 'fixture adapter unavailable');
    source.stack = 'AdapterUnavailableError: fixture adapter unavailable\n    at plugin (fixture.js:1:1)';

    const wrapped = wrapError(source);

    expect(wrapped.code).toBe('KB_ADAPTER_NOT_CONFIGURED');
    expect(wrapped.hint).toBe(toErrorEnvelope(source).hint);
    expect(wrapped.details).toEqual({ service: 'llm', adapter: 'platform.adapters.llm', command: expect.any(String) });
    expect(wrapped.stack).toBe(source.stack);
    expect(validateErrorEnvelope(toErrorEnvelope(wrapped))).toEqual([]);
  });

  it('keeps a validated product plugin code and rejects a missing hint', () => {
    const productError = createProductErrorEnvelope({
      code: 'COMMIT_NOTHING_STAGED',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
      retryable: false,
    });
    const error = new PluginError(
      'Nothing is staged.',
      productError.code,
      undefined,
      'Stage the intended changes, then retry.',
    );
    const restored = PluginError.fromJSON(error.toJSON());

    expect(toErrorEnvelope(restored)).toMatchObject({
      code: 'COMMIT_NOTHING_STAGED',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
      area: 'product',
    });
    expect(toErrorEnvelope(new PluginError('Nothing is staged.', productError.code))).toMatchObject({
      code: 'KB_PLUGIN_CONTRACT_VIOLATION',
    });
  });
});
