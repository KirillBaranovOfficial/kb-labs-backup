import { getCatalogEntry } from '@kb-labs/core-platform/error-envelope';

const SAFE_FALLBACK_CODE = 'KB_RUNTIME_UNEXPECTED';

export function resolveHandlerErrorCode(error: unknown): string {
  if (typeof error !== 'object' || error === null) return SAFE_FALLBACK_CODE;
  const candidate = 'code' in error && typeof error.code === 'string'
    ? error.code
    : 'errorCode' in error && typeof error.errorCode === 'string'
      ? error.errorCode
      : undefined;
  return candidate && getCatalogEntry(candidate) ? candidate : SAFE_FALLBACK_CODE;
}
