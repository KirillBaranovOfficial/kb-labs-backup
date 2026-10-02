export interface StudioFailure {
  code: string;
  message: string;
  hint: string;
  correlationId: string;
  stack?: string;
}

interface ErrorEnvelopeLike {
  code: string;
  message: string;
  hint: string;
  correlationId?: string;
}

const SAFE_UNEXPECTED_MESSAGE = 'The operation failed unexpectedly.';
const SAFE_UNEXPECTED_HINT = 'Copy diagnostics and contact support.';

export function normalizeStudioFailure(value: unknown): StudioFailure {
  const envelope = findEnvelope(value);
  const error = value instanceof Error ? value : undefined;

  if (envelope) {
    return {
      code: envelope.code,
      message: envelope.message,
      hint: envelope.hint,
      correlationId: envelope.correlationId || createCorrelationId(),
      stack: error?.stack,
    };
  }

  return {
    code: 'KB_RUNTIME_UNEXPECTED',
    message: SAFE_UNEXPECTED_MESSAGE,
    hint: SAFE_UNEXPECTED_HINT,
    correlationId: createCorrelationId(),
    stack: error?.stack,
  };
}

export function recordStudioFailure(message: string, failure: StudioFailure): void {
  console.debug(JSON.stringify({
    time: new Date().toISOString(),
    level: 'error',
    category: 'studio:error-boundary',
    msg: message,
    code: failure.code,
    correlationId: failure.correlationId,
    stack: failure.stack,
  }));
}

function findEnvelope(value: unknown): ErrorEnvelopeLike | undefined {
  if (!isRecord(value)) {
    return undefined;
  }

  const nestedError = isRecord(value.error) ? value.error : value;
  if (
    typeof nestedError.code === 'string' && nestedError.code.startsWith('KB_') &&
    typeof nestedError.message === 'string' &&
    typeof nestedError.hint === 'string'
  ) {
    return {
      code: nestedError.code,
      message: nestedError.message,
      hint: nestedError.hint,
      correlationId: typeof nestedError.correlationId === 'string'
        ? nestedError.correlationId
        : undefined,
    };
  }

  return undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function createCorrelationId(): string {
  const id = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2);
  return `studio-${id}`;
}
