import { describe, expect, it, vi } from 'vitest';
import { dispatchWithPlatformAuthGate } from './platform-auth-gate.js';
import { isPlatformAuthExemptFlag } from './platform-auth-policy.js';

describe('platform authentication gate', () => {
  // case: S-041.7
  it('blocks an undeclared plugin before its handler is called', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'fresh-plugin run', commandKind: 'plugin', hasAuth: false,
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
      accessMode: 'secured', commandPath: 'project list', commandKind: 'system', hasAuth: false,
      gatewayUrl: 'http://127.0.0.1:4000',
    }, handler);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      message: 'Authentication is required.',
      hint: 'Run "kb auth login --gateway-url http://127.0.0.1:4000 --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
    } });
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
      accessMode: 'secured', commandPath: 'project list', commandKind: 'system', hasAuth: false, authError: hostError,
    }, async () => 0);
    expect(result).toEqual({ error: hostError });
  });

  // case: S-041.7
  it('blocks protected platform commands with the same envelope before dispatch', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'config show', commandKind: 'system', hasAuth: false,
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
      accessMode: 'secured', commandPath: 'auth tokens', commandKind: 'system', hasAuth: false,
      gatewayUrl: 'https://gateway.example.test',
    }, async () => 0);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      hint: 'Run "kb auth login --gateway-url https://gateway.example.test --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
    } });
  });

  // case: S-041.7
  it('does not show an unresolved URL placeholder when no Gateway address is configured', async () => {
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'config show', commandKind: 'system', hasAuth: false,
    }, async () => 0);
    expect(result).toMatchObject({ error: {
      code: 'KB_AUTH_REQUIRED',
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
    } });
    expect(JSON.stringify(result)).not.toContain('<url>');
  });

  // case: S-041.8
  it('allows only platform-owned system commands without a session', async () => {
    for (const commandPath of ['auth login', 'auth logout', 'auth status', 'auth register', 'info version', 'info health', 'diag']) {
      const handler = vi.fn(async () => 0);
      const result = await dispatchWithPlatformAuthGate({
        accessMode: 'secured', commandPath, commandKind: 'system', hasAuth: false,
      }, handler);
      expect(result).toEqual({ value: 0 });
      expect(handler).toHaveBeenCalledOnce();
    }
  });

  it('does not let a plugin widen the system allowlist by choosing an auth command path', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate({
      accessMode: 'secured', commandPath: 'auth login', commandKind: 'plugin', hasAuth: false,
    }, handler);
    expect(result).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    expect(handler).not.toHaveBeenCalled();
  });

  it('allows help output and every local-mode command without a session', async () => {
    const handler = vi.fn(async () => 0);
    for (const input of [
      { accessMode: 'secured', commandPath: 'plugin run', commandKind: 'plugin', hasAuth: false, help: true },
      { accessMode: 'local', commandPath: 'plugin run', commandKind: 'plugin', hasAuth: false },
    ] as const) {
      await dispatchWithPlatformAuthGate(input, handler);
    }
    expect(handler).toHaveBeenCalledTimes(2);
    expect(isPlatformAuthExemptFlag('help')).toBe(true);
    expect(isPlatformAuthExemptFlag('version')).toBe(true);
  });
});
