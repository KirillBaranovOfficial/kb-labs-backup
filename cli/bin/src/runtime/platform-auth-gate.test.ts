import { describe, expect, it, vi } from 'vitest';
import { createErrorEnvelope } from '@kb-labs/core-platform';
import { dispatchWithPlatformAuthGate } from './platform-auth-gate.js';
import { isPlatformAuthExemptFlag } from './platform-auth-policy.js';

describe('platform authentication gate', () => {
  // case: S-041.7
  it('blocks an undeclared plugin before its handler is called', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'fresh-plugin run', commandKind: 'plugin', requiresSession: true, hasAuth: false,
    }, handler);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      message: 'Authentication is required.',
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
    } });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-041.4
  it('requires login when the host rejects a saved user token', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'project list', commandKind: 'system', requiresSession: true, hasAuth: false,
      gatewayUrl: 'http://127.0.0.1:4000',
    }, handler);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      message: 'Authentication is required.',
      hint: createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: 'http://127.0.0.1:4000' } }).hint,
    } });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-041.17
  it('denies protected commands when the platform access mode is unresolved', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'unknown', commandPath: 'project add', commandKind: 'system', requiresSession: true,
      hasAuth: false, gatewayUrl: 'http://127.0.0.1:4000',
    }, handler);
    const required = createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: 'http://127.0.0.1:4000' } });
    expect(result).toEqual({ error: required });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-203.14
  it('returns a host connectivity error when auth verification cannot reach the Gateway', async () => {
    const hostError = {
      code: 'KB_HOST_UNREACHABLE', area: 'host', stage: 'run', severity: 'error', retryable: true,
      message: 'The KB Labs host is not running: cannot connect to http://127.0.0.1:4000.',
      hint: 'Run "kb-create start", or "kb-create doctor" if the host should already be running.',
      details: { address: 'http://127.0.0.1:4000' }, correlationId: 'host-down',
    } as const;
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'project list', commandKind: 'system', requiresSession: true, hasAuth: false, authError: hostError,
    }, async () => 0);
    expect(result).toEqual({ error: hostError });
  });

  // case: S-041.7
  it('blocks protected platform commands with the same envelope before dispatch', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'config show', commandKind: 'system', requiresSession: true, hasAuth: false,
    }, handler);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      message: 'Authentication is required.',
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
    } });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-041.7
  it('blocks the secured platform marketplace list before gateway-dependent dispatch', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured',
      commandPath: 'marketplace plugins list',
      commandKind: 'plugin',
      requiresSession: true,
      hasAuth: false,
    }, handler);

    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      message: 'Authentication is required.',
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
    } });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-031.4
  it('uses the configured Gateway URL in the login recovery command', async () => {
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'auth tokens', commandKind: 'system', requiresSession: true, hasAuth: false,
      gatewayUrl: 'https://gateway.example.test',
    }, async () => 0);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      hint: createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: 'https://gateway.example.test' } }).hint,
    } });
  });

  // case: S-041.7
  it('does not show an unresolved URL placeholder when no Gateway address is configured', async () => {
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'config show', commandKind: 'system', requiresSession: true, hasAuth: false,
    }, async () => 0);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      details: { gatewayUrl: 'unconfigured' },
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
    } });
    expect(JSON.stringify(result)).not.toContain('<url>');
  });

  // case: S-041.8
  it('allows commands only when the registry marks them as not session-protected', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'auth login', commandKind: 'system',
      requiresSession: false, hasAuth: false,
    }, handler);
    expect(result).toEqual({ value: 0 });
    expect(handler).toHaveBeenCalledOnce();
  });

  it('does not let a plugin widen the system allowlist by choosing an auth command path', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'auth login', commandKind: 'plugin', requiresSession: true, hasAuth: false,
    }, handler);
    expect(result).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    expect(handler).not.toHaveBeenCalled();
  });

  it('allows help output and every local-mode command without a session', async () => {
    const handler = vi.fn(async () => 0);
    for (const input of [
      { accessMode: 'secured', commandPath: 'plugin run', commandKind: 'plugin', requiresSession: true, hasAuth: false, help: true },
      { accessMode: 'local', commandPath: 'plugin run', commandKind: 'plugin', requiresSession: true, hasAuth: false },
    ] as const) {
      await dispatchWithPlatformAuthGate(input, handler);
    }
    expect(handler).toHaveBeenCalledTimes(2);
    expect(isPlatformAuthExemptFlag('help')).toBe(true);
    expect(isPlatformAuthExemptFlag('version')).toBe(true);
  });
});
