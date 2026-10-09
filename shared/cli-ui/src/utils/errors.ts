import { isCataloguedErrorCode, toErrorEnvelope } from '@kb-labs/plugin-contracts';
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
export function handleError(err: unknown): CommandFailure {
  const envelope = err !== null && typeof err === 'object' && 'envelope' in err
    ? err.envelope
    : null;
  if (envelope !== null && typeof envelope === 'object' && 'code' in envelope && 'message' in envelope) {
    const typedEnvelope = envelope as { code: string; message: string; cause?: string; hint?: string; details?: Record<string, unknown>; diagnostics?: CommandError['diagnostics']; [key: string]: unknown };
    const code = isCataloguedErrorCode(typedEnvelope.code) ? typedEnvelope.code : 'KB_RUNTIME_COMMAND_FAILED';
    return {
      ok: false,
      error: {
        code,
        message: typedEnvelope.message,
        ...(typedEnvelope.cause ? { cause: typedEnvelope.cause } : {}),
        hint: typedEnvelope.hint ?? 'Resolve the reported error, then retry the command.',
        ...(typedEnvelope.details && isStringMap(typedEnvelope.details) ? { details: typedEnvelope.details } : {}),
        ...(typedEnvelope.diagnostics ? { diagnostics: typedEnvelope.diagnostics } : {}),
      },
    };
  }
  const typedEnvelope = toErrorEnvelope(err);
  const code = isCataloguedErrorCode(typedEnvelope.code) ? typedEnvelope.code : 'KB_RUNTIME_COMMAND_FAILED';
  return {
    ok: false,
    error: {
      code,
      message: typedEnvelope.message,
      ...(typedEnvelope.cause ? { cause: typedEnvelope.cause } : {}),
      hint: typedEnvelope.hint,
      ...(typedEnvelope.details && isStringMap(typedEnvelope.details) ? { details: typedEnvelope.details } : {}),
      ...(typedEnvelope.diagnostics ? { diagnostics: typedEnvelope.diagnostics } : {}),
    },
  };
}

function isStringMap(value: unknown): value is Record<string, string> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) &&
    Object.values(value).every((item) => typeof item === 'string');
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
