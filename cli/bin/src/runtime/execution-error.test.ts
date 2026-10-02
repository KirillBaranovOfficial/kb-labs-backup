import { describe, expect, it, vi } from 'vitest';
import { ServicePortConfigError } from '@kb-labs/core-platform';
import type { Presenter } from '@kb-labs/cli-runtime';
import { handleExecutionError } from './execution-error.js';

describe('handleExecutionError', () => {
  // case: S-203.16
  it('renders invalid service ports as catalogued JSON errors', () => {
    const presenter = {
      isJSON: true,
      json: vi.fn(),
      error: vi.fn(),
    } as unknown as Presenter;

    const exitCode = handleExecutionError(
      new ServicePortConfigError('GATEWAY_PORT', 'must be an integer from 1 through 65535'),
      presenter,
    );

    expect(exitCode).toBe(1);
    expect(presenter.json).toHaveBeenCalledWith({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_CONFIG_INVALID',
        message: 'Environment variable "GATEWAY_PORT" must be an integer from 1 through 65535.',
        hint: 'Run "unset GATEWAY_PORT", then retry the command to use the service default port.',
      }),
    });
  });
});
