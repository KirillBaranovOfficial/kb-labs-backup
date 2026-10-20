import { toErrorEnvelope } from '@kb-labs/core-platform/error-envelope';

export interface StudioFailure {
  code: string;
  message: string;
  hint: string;
  correlationId: string;
  stack?: string;
}

const SAFE_UNEXPECTED_MESSAGE = 'The operation failed unexpectedly.';
const SAFE_UNEXPECTED_HINT = 'Copy diagnostics and contact support.';

export function normalizeStudioFailure(value: unknown): StudioFailure {
  const source = isRecord(value) && isRecord(value.error) ? value.error : value;
  const envelope = toErrorEnvelope(source);
  const error = value instanceof Error ? value : undefined;
  const sourceCorrelationId = isRecord(source) && typeof source.correlationId === 'string'
    ? source.correlationId
    : isRecord(source) && isRecord(source.envelope) && typeof source.envelope.correlationId === 'string'
      ? source.envelope.correlationId
      : undefined;

  return {
    code: envelope.code,
    message: envelope.code === 'KB_RUNTIME_UNEXPECTED' ? SAFE_UNEXPECTED_MESSAGE : envelope.message,
    hint: envelope.code === 'KB_RUNTIME_UNEXPECTED' ? SAFE_UNEXPECTED_HINT : envelope.hint,
    correlationId: sourceCorrelationId || createCorrelationId(),
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function createCorrelationId(): string {
  const id = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2);
  return `studio-${id}`;
}
