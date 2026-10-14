import {
  ErrorEnvelopeError,
  toErrorEnvelope,
} from '@kb-labs/core-platform';
import type { ErrorEnvelope } from '@kb-labs/core-platform';

export type SerializedCataloguedError = ErrorEnvelope;

export function serializeCataloguedError(error: unknown): SerializedCataloguedError {
  const envelope = toErrorEnvelope(error);
  return envelope;
}

export function rebuildCataloguedError(error: SerializedCataloguedError): ErrorEnvelopeError {
  return new ErrorEnvelopeError(toErrorEnvelope(error));
}
