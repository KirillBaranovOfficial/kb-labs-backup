import { describe, expect, it, vi } from 'vitest';
import { handleMarketplaceError } from '../errors.js';

describe('marketplace CLI errors', () => {
  it.each([
    ['KB_PLUGIN_NOT_FOUND', 'Plugin @kb-labs/not-published was not found in the registry.', 'kb marketplace search'],
    ['KB_ADAPTER_NOT_INSTALLED', 'Adapter package @kb-labs/not-installed is not installed on this platform.', 'kb marketplace install @kb-labs/not-installed --scope platform'],
  ])('presents %s through the shared human renderer and JSON envelope', (code, message, hint) => {
    const json = vi.fn();
    const output = vi.fn();
    const envelope = { code, message, hint };
    const error = { envelope };

    handleMarketplaceError({ ui: { json, error: output } }, error, true);
    expect(json).toHaveBeenCalledWith({ ok: false, error: envelope });

    handleMarketplaceError({ ui: { json, error: output } }, error);
    expect(output).toHaveBeenCalledWith(`${message} [${code}]`, { hint });
  });

  // case: S-041.7
  it('renders missing authentication as a typed error envelope', () => {
    const json = vi.fn();
    const ctx = { ui: { json } };

    handleMarketplaceError(ctx, {
      envelope: {
        code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
        message: 'Authentication is required.', hint: 'Run "kb auth login".',
      },
    }, true);

    expect(json).toHaveBeenCalledWith({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        area: 'auth',
        stage: 'run',
        severity: 'error',
        retryable: false,
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
      },
    });
  });
});
