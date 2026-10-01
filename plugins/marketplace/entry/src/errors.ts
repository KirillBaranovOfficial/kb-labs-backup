interface MarketplaceErrorContext {
  ui?: {
    json?: (payload: unknown) => void;
    error?: (message: string, options?: { hint?: string }) => void;
  };
}

export function handleMarketplaceError(ctx: MarketplaceErrorContext, error: unknown, isJson?: boolean): void {
  const envelope = error !== null && typeof error === 'object' && 'envelope' in error
    ? error.envelope
    : null;
  if (envelope !== null && typeof envelope === 'object' && 'code' in envelope && 'message' in envelope) {
    const typed = envelope as { code: string; message: string; hint?: string; [key: string]: unknown };
    if (isJson) {
      ctx.ui?.json?.({ ok: false, error: typed });
    } else {
      ctx.ui?.error?.(`${typed.code}: ${typed.message}`, { hint: typed.hint });
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
