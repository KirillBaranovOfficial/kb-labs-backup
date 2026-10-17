import type { ProjectRegistryErrorCode } from '@kb-labs/core-contracts';
import type { DetailsFor, PlatformErrorCode } from '@kb-labs/core-platform';

/**
 * Typed registry failure. `code` is a stable catalog code; `details` holds the
 * secret-free values that fill the catalog message/hint templates.
 * Command layers convert it to an error envelope with `createErrorEnvelope`.
 */
export class ProjectRegistryError<C extends ProjectRegistryErrorCode = ProjectRegistryErrorCode> extends Error {
  readonly code: C;
  readonly details: DetailsFor<C & PlatformErrorCode>;

  constructor(code: C, message: string, details: DetailsFor<C & PlatformErrorCode>) {
    super(message);
    this.name = 'ProjectRegistryError';
    this.code = code;
    this.details = details;
  }
}

export function isProjectRegistryError(value: unknown): value is ProjectRegistryError<ProjectRegistryErrorCode> {
  return value instanceof ProjectRegistryError;
}
