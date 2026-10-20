import { describe, expect, it } from 'vitest';
import { createEnvShim } from '../runtime/env-shim.js';

describe('plugin runtime environment overrides', () => {
  // case: S-010.7
  it('reads explicit execution environment values without changing process.env', () => {
    const previousGatewayUrl = process.env.KB_GATEWAY_URL;
    const env = createEnvShim({
      permissions: { env: { read: ['KB_GATEWAY_URL'] } },
      environment: { KB_GATEWAY_URL: 'http://127.0.0.1:43123' },
    });

    expect(env('KB_GATEWAY_URL')).toBe('http://127.0.0.1:43123');
    expect(process.env.KB_GATEWAY_URL).toBe(previousGatewayUrl);
  });
});
