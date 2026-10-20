import { describe, expect, it } from 'vitest';
import { requireEnvironmentSecret } from './required-secret.js';

describe('required service secrets', () => {
  it('fails in every mode with a catalog diagnostic naming the variable and next action', () => {
    expect(() => requireEnvironmentSecret('GATEWAY_INTERNAL_SECRET', {})).toThrow(
      'KB_CONFIG_SECRET_MISSING: The configuration references GATEWAY_INTERNAL_SECRET, which has no value. Hint: set GATEWAY_INTERNAL_SECRET in the deployment secret configuration, then restart the service.',
    );
  });

  it('returns a configured secret unchanged', () => {
    expect(requireEnvironmentSecret('GATEWAY_INTERNAL_SECRET', { GATEWAY_INTERNAL_SECRET: 'configured' }))
      .toBe('configured');
  });
});
