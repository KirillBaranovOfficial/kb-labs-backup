import { describe, expect, it } from 'vitest';
import { ClickUpApiError } from '@kb-labs/clickup-core';
import { handleError } from '../../utils/error.js';

describe('ClickUp command errors', () => {
  it('uses a catalogued authorization failure when ClickUp refuses the identity', () => {
    const result = handleError(new ClickUpApiError(403, 'OAUTH_017', 'Access denied'));

    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe('KB_AUTH_FORBIDDEN');
    }
  });
});
