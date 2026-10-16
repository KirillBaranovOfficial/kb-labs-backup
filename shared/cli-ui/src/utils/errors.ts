import { toErrorEnvelope } from '@kb-labs/plugin-contracts';
import type { CommandFailure } from '@kb-labs/plugin-contracts';
import type { CommandError } from '@kb-labs/plugin-contracts';

/** Render a platform failure consistently for system and plugin CLI commands. */
export function renderErrorEnvelope(envelope: CommandError): string {
  return [
    `${envelope.message} [${envelope.code}]`,
    ...(envelope.cause ? [`  Cause: ${envelope.cause}`] : []),
    ...(envelope.diagnostics ?? []).map((item) => `  - ${item.code} (${item.severity})${item.path ? ` ${item.path}${item.line ? `:${item.line}` : ''}` : ''}: ${item.message}${item.hint ? ` — ${item.hint}` : ''}`),
    `  Hint:  ${envelope.hint}`,
    ...(envelope.correlationId ? [`  Correlation ID: ${envelope.correlationId}`] : []),
  ].join('\n');
}

/**
 * Return a typed validation failure for the shared command renderer.
 * Call before try/catch, right before returning the command result.
 */
export function validationError(
  message: string,
  hint?: string,
): CommandFailure {
  return {
    ok: false,
    error: {
      code: 'KB_RUNTIME_INPUT_INVALID',
      message,
      hint: hint ?? 'Run the command with --help to check its required arguments.',
    },
  };
}

/**
 * Return a typed runtime/API failure for the shared command renderer.
 * Call in catch blocks, right before returning the command result.
 */
export function handleError(err: unknown, correlationId?: string): CommandFailure {
  const source = err !== null && typeof err === 'object' && 'envelope' in err ? err.envelope : err;
  const typedEnvelope = toErrorEnvelope(source, correlationId ? { correlationId } : {});
  return {
    ok: false,
    error: {
      code: typedEnvelope.code,
      message: typedEnvelope.message,
      ...(typedEnvelope.cause ? { cause: typedEnvelope.cause } : {}),
      hint: typedEnvelope.hint,
      ...(typedEnvelope.correlationId ? { correlationId: typedEnvelope.correlationId } : {}),
      ...(typedEnvelope.details ? { details: typedEnvelope.details } : {}),
      ...(typedEnvelope.diagnostics ? { diagnostics: typedEnvelope.diagnostics } : {}),
    },
  };
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
