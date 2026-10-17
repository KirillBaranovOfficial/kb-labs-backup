import type { ErrorEnvelope } from './types.js';

const validatedErrorEnvelope = Symbol('validatedErrorEnvelope');

/** Envelope that crossed a core-platform validation boundary. */
export type ValidatedErrorEnvelope = ErrorEnvelope & {
  readonly [validatedErrorEnvelope]: true;
};

/** Internal marker used only after the catalogue validator accepts an envelope. */
export function markValidatedErrorEnvelope<T extends ErrorEnvelope>(envelope: T): T & ValidatedErrorEnvelope {
  const validated = Object.assign(envelope, { [validatedErrorEnvelope]: true as const });
  Object.defineProperty(validated, validatedErrorEnvelope, { enumerable: false });
  return validated;
}
