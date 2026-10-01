import { describe, expect, it, vi } from 'vitest';
import { dispatchWithPlatformAuthGate, platformAuthFailure } from './platform-auth-gate.js';

describe('platform authentication gate', () => {
  // case: S-041.7, S-041.10
  it('returns the catalog auth envelope before a declared secured command dispatches', () => {
    expect(platformAuthFailure({ accessMode: 'secured', requiresPlatformAuth: true, hasAuth: false }))
      .toMatchObject({ code: 'KB_AUTH_REQUIRED', hint: 'Run "kb auth login".' });
  });

  // case: S-041.7
  it('does not call the handler when secured authentication is missing', async () => {
    const handler = vi.fn(async () => 0);
    const result = await dispatchWithPlatformAuthGate(
      { accessMode: 'secured', requiresPlatformAuth: true, hasAuth: false }, handler,
    );
    expect(result).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    expect(handler).not.toHaveBeenCalled();
  });

  // case: S-041.10
  it('allows exempt commands, authenticated commands, and every local command', () => {
    expect(platformAuthFailure({ accessMode: 'secured', requiresPlatformAuth: false, hasAuth: false })).toBeNull();
    expect(platformAuthFailure({ accessMode: 'secured', requiresPlatformAuth: true, hasAuth: true })).toBeNull();
    expect(platformAuthFailure({ accessMode: 'local', requiresPlatformAuth: true, hasAuth: false })).toBeNull();
  });

  // case: S-041.10
  it('dispatches exempt, authenticated, and local commands', async () => {
    const handler = vi.fn(async () => 0);
    for (const input of [
      { accessMode: 'secured', requiresPlatformAuth: false, hasAuth: false },
      { accessMode: 'secured', requiresPlatformAuth: true, hasAuth: true },
      { accessMode: 'local', requiresPlatformAuth: true, hasAuth: false },
    ]) {
      await dispatchWithPlatformAuthGate(input, handler);
    }
    expect(handler).toHaveBeenCalledTimes(3);
  });
});
