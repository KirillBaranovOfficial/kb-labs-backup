import { describe, expect, it } from 'vitest';
import { toErrorEnvelope, validateErrorEnvelope } from '@kb-labs/core-platform';
import {
  AbortError,
  ConfigError,
  PermissionError,
  PlatformError,
  PluginError,
  RateLimitError,
  TimeoutError,
  ValidationError,
} from './errors.js';
describe('core toErrorEnvelope for plugin errors', () => {
  const cases: Array<[string, PluginError, string]> = [
    ['validation', new ValidationError('bad', { field: 'cwd' }), 'KB_RUNTIME_INPUT_INVALID'],
    ['config', new ConfigError('bad config', { path: 'llm.model' }), 'KB_CONFIG_INVALID'],
    ['timeout', new TimeoutError('slow'), 'KB_RUNTIME_TIMEOUT'],
    ['permission', new PermissionError('no', { plugin: 'commit', permission: 'fs.write' }), 'KB_PLUGIN_PERMISSION_DENIED'],
    ['rate limit', new RateLimitError('slow down', 2500), 'KB_RUNTIME_RATE_LIMITED'],
    ['platform', new PlatformError('llm', 'down'), 'KB_HOST_ADAPTER_UNAVAILABLE'],
    ['abort', new AbortError(), 'PLUGIN_ABORTED'],
    ['custom', new PluginError('boom', 'COMMIT_PUSH_REJECTED'), 'COMMIT_PUSH_REJECTED'],
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
    expect(toErrorEnvelope(new ConfigError('x', { path: 'llm.model' })).message).toContain('llm.model');
    expect(toErrorEnvelope(new RateLimitError('x', 2500)).message).toContain('3s');
  });

  it('rejects structured details rather than serializing them into strings', () => {
    const envelope = toErrorEnvelope(new PluginError('bad', 'CUSTOM_FAILURE', {
      report: { findings: [] },
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
});
