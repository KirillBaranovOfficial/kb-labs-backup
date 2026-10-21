/**
 * @module @kb-labs/sdk/errors
 * Platform-owned error envelope and catalogue contracts for plugin authors.
 */

export type {
  ErrorEnvelope,
  ErrorAction,
  ErrorCatalogEntry,
  PlatformErrorCode,
  DetailsOptionFor,
} from '@kb-labs/core-platform/error-envelope';

export {
  errorCatalog,
  getCatalogEntry,
  isErrorCode,
  createValidatedErrorEnvelope,
} from '@kb-labs/core-platform/error-envelope';
