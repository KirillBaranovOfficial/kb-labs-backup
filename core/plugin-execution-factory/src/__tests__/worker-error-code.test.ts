import { describe, expect, it } from 'vitest';
import { resolveHandlerErrorCode } from '../backends/worker-pool/resolve-handler-error-code.js';

describe('resolveHandlerErrorCode', () => {
  it('preserves a code from the error catalog', () => {
    expect(resolveHandlerErrorCode({ code: 'KB_RUNTIME_UNEXPECTED' })).toBe('KB_RUNTIME_UNEXPECTED');
  });

  it('maps an arbitrary thrown code to the safe catalog fallback', () => {
    expect(resolveHandlerErrorCode({ code: 'PRIVATE_INTERNAL_CODE' })).toBe('KB_RUNTIME_UNEXPECTED');
  });
});
