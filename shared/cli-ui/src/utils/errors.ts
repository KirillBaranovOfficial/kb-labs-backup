import { toPluginErrorEnvelope } from '@kb-labs/plugin-contracts';

interface ErrorContext {
  ui?: {
    json?: (data: unknown) => void;
    error?: (message: string, opts?: Record<string, unknown>) => void;
  };
}

/**
 * Structured validation error — emits { ok: false, error: { code: 'INVALID_ARGS', ... } } in JSON mode.
 * Call before try/catch, right before `return { exitCode: 1 }`.
 */
export function validationError(
  ctx: ErrorContext,
  message: string,
  hint?: string,
  isJson?: boolean,
): void {
  if (isJson) {
    ctx.ui?.json?.({ ok: false, error: { code: 'INVALID_ARGS', message, ...(hint && { hint }) } });
  } else {
    ctx.ui?.error?.(message, { ...(hint && { hint }) });
  }
}

/**
 * Structured runtime/API error — emits { ok: false, error: { code: 'INTERNAL_ERROR', ... } } in JSON mode.
 * Call in catch blocks, right before `return { exitCode: 1 }`.
 */
export function handleError(ctx: ErrorContext, err: unknown, isJson?: boolean): void {
  const envelope = err !== null && typeof err === 'object' && 'envelope' in err
    ? err.envelope
    : null;
  if (envelope !== null && typeof envelope === 'object' && 'code' in envelope && 'message' in envelope) {
    const typedEnvelope = envelope as { code: string; message: string; hint?: string; details?: Record<string, unknown>; [key: string]: unknown };
    if (isJson) {
      ctx.ui?.json?.({ ok: false, error: typedEnvelope });
    } else {
      const permission = typeof typedEnvelope.details?.permission === 'string' ? typedEnvelope.details.permission : undefined;
      const cause = typeof typedEnvelope.cause === 'string' ? typedEnvelope.cause : undefined;
      ctx.ui?.error?.(`${typedEnvelope.message}${permission ? ` (permission: ${permission})` : ''} [${typedEnvelope.code}]`, {
        ...(cause ? { cause } : {}),
        ...(typedEnvelope.hint ? { hint: typedEnvelope.hint } : {}),
      });
    }
    return;
  }
  const typedEnvelope = toPluginErrorEnvelope(err);
  if (isJson) {
    ctx.ui?.json?.({ ok: false, error: typedEnvelope });
  } else {
    const permission = typeof typedEnvelope.details?.permission === 'string'
      ? typedEnvelope.details.permission
      : undefined;
    const cause = typeof typedEnvelope.cause === 'string' ? typedEnvelope.cause : undefined;
    ctx.ui?.error?.(
      `${typedEnvelope.message}${permission ? ` (permission: ${permission})` : ''} [${typedEnvelope.code}]`,
      { ...(cause ? { cause } : {}), hint: typedEnvelope.hint },
    );
  }
}

/**
 * Re-throws `err` as an HTTP-aware error for REST handlers.
 * Envelope middleware reads `err.statusCode` to set HTTP status.
 * If err already has statusCode — preserves it. Otherwise → 500.
 */
export function rethrowForRest(err: unknown): never {
  if (err !== null && typeof err === 'object' && 'statusCode' in err) {
    throw err;
  }
  const message = err instanceof Error ? err.message : String(err);
  throw Object.assign(new Error(message), {
    statusCode: 500,
    code: 'INTERNAL_ERROR',
  });
}
