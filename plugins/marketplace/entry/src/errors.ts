interface MarketplaceErrorContext {
  ui?: {
    json?: (payload: unknown) => void;
    error?: (message: string, options?: { hint?: string }) => void;
  };
}

export class MarketplaceAuthRequiredError extends Error {
  readonly code = 'KB_AUTH_REQUIRED';
  readonly hint = 'Run "kb auth login".';

  constructor() {
    super('Authentication is required.');
    this.name = 'MarketplaceAuthRequiredError';
  }
}

export function handleMarketplaceError(ctx: MarketplaceErrorContext, error: unknown, isJson?: boolean): void {
  if (error instanceof MarketplaceAuthRequiredError) {
    if (isJson) {
      ctx.ui?.json?.({
        ok: false,
        error: { code: error.code, message: error.message, hint: error.hint },
      });
    } else {
      ctx.ui?.error?.(error.message, { hint: error.hint });
    }
    return;
  }
  const message = error instanceof Error ? error.message : String(error);
  if (isJson) {
    ctx.ui?.json?.({ ok: false, error: { code: 'INTERNAL_ERROR', message } });
  } else {
    ctx.ui?.error?.(message);
  }
}
