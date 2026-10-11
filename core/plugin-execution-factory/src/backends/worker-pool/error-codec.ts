import {
  ErrorEnvelopeError,
  toErrorEnvelope,
} from '@kb-labs/core-platform';
import type { ErrorEnvelope } from '@kb-labs/core-platform';

export type SerializedCataloguedError = Pick<ErrorEnvelope, 'code' | 'message' | 'hint'>
  & Partial<Pick<ErrorEnvelope, 'cause' | 'details'>>;

export function serializeCataloguedError(error: unknown): SerializedCataloguedError {
  const envelope = toErrorEnvelope(error);
  return {
    code: envelope.code,
    message: envelope.message,
    hint: envelope.hint,
    ...(envelope.cause !== undefined ? { cause: envelope.cause } : {}),
    ...(envelope.details !== undefined ? { details: envelope.details } : {}),
  };
}

export function rebuildCataloguedError(error: SerializedCataloguedError): ErrorEnvelopeError {
  return new ErrorEnvelopeError(toErrorEnvelope(error));
}
