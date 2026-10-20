import { createValidatedErrorEnvelope, type DetailsFor, type ErrorEnvelope, type PlatformErrorCode } from '@kb-labs/core-platform';

export type ExtensionStoreErrorCode =
  | 'KB_PLUGIN_INTEGRITY_MISMATCH'
  | 'KB_PLUGIN_INSTALL_FAILED'
  | 'KB_RUNTIME_INPUT_INVALID';

/**
 * Typed store failure. `code` is a stable catalog code; `details` holds the
 * secret-free values that fill the catalog message/hint templates. Command
 * layers convert it to an error envelope with `createErrorEnvelope`.
 */
export class ExtensionStoreError<C extends ExtensionStoreErrorCode> extends Error {
  readonly code: C;
  readonly envelope: ErrorEnvelope;
  readonly details: DetailsFor<C & PlatformErrorCode>;

  constructor(code: C, details: DetailsFor<C & PlatformErrorCode>, cause?: string) {
    const envelope = createValidatedErrorEnvelope(code, { details, cause });
    super(`${envelope.code}: ${envelope.message} ${envelope.hint}`);
    this.name = 'ExtensionStoreError';
    this.code = code;
    this.envelope = envelope;
    this.details = details;
  }
}

/**
 * A store entry's recorded integrity does not match what the caller expected
 * (a project's pin, or the value observed right after install). Carries
 * `KB_PLUGIN_INTEGRITY_MISMATCH`.
 */
export class IntegrityMismatchError extends ExtensionStoreError<'KB_PLUGIN_INTEGRITY_MISMATCH'> {
  constructor(details: { plugin: string; expected: string; actual: string }) {
    super(
      'KB_PLUGIN_INTEGRITY_MISMATCH',
      details,
      `Store entry for "${details.plugin}" has integrity ${details.actual}, expected ${details.expected}.`,
    );
    this.name = 'IntegrityMismatchError';
  }
}

/**
 * The package manager failed to install a version into the store (network,
 * registry, or a bad spec). Carries `KB_PLUGIN_INSTALL_FAILED`.
 */
export class StoreInstallError extends ExtensionStoreError<'KB_PLUGIN_INSTALL_FAILED'> {
  constructor(details: { plugin: string }, cause: string) {
    super('KB_PLUGIN_INSTALL_FAILED', details, cause);
    this.name = 'StoreInstallError';
  }
}

export function isExtensionStoreError(value: unknown): value is ExtensionStoreError<ExtensionStoreErrorCode> {
  return value instanceof ExtensionStoreError;
}
