import { describe, expect, it, vi } from 'vitest';
import { handleMarketplaceError, MarketplaceAuthRequiredError } from '../errors.js';

describe('marketplace CLI errors', () => {
  // case: S-010.6
  it('renders missing authentication as a typed error envelope', () => {
    const json = vi.fn();
    const ctx = { ui: { json } };

    handleMarketplaceError(ctx, new MarketplaceAuthRequiredError(), true);

    expect(json).toHaveBeenCalledWith({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
      },
    });
  });
});
