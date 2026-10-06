import { describe, expect, it, vi } from 'vitest';
import { handleError } from '@kb-labs/sdk';

describe('marketplace CLI errors', () => {
  it.each([
    ['KB_PLUGIN_NOT_FOUND', 'Plugin @kb-labs/not-published was not found in the registry.', 'kb marketplace search'],
    ['KB_ADAPTER_NOT_INSTALLED', 'Adapter package @kb-labs/not-installed is not installed on this platform.', 'kb marketplace install @kb-labs/not-installed --scope platform'],
  ])('returns %s through the typed failure contract without printing', (code, message, hint) => {
    const json = vi.fn();
    const output = vi.fn();
    const envelope = { code, message, hint };
    const error = { envelope };

    const result = handleError({ ui: { json, error: output } }, error, true);
    expect(result).toMatchObject({ ok: false, error: envelope });

    expect(handleError({ ui: { json, error: output } }, error)).toMatchObject({ ok: false, error: envelope });
    expect(json).not.toHaveBeenCalled();
    expect(output).not.toHaveBeenCalled();
  });

  // case: S-041.7
  it('renders missing authentication as a typed error envelope', () => {
    const json = vi.fn();
    const ctx = { ui: { json } };

    const result = handleError(ctx, {
      envelope: {
        code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
        message: 'Authentication is required.', hint: 'Run "kb auth login".',
      },
    }, true);

    expect(result).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
      },
    });
    expect(json).not.toHaveBeenCalled();
  });
});
