import { describe, expect, it } from 'vitest';

describe('core-contracts export surface snapshot', () => {
  it('matches entrypoint export keys', () => {
    const expectedSurface = [
      'CANONICAL_OBSERVABILITY_METRICS',
      'ExecutionStats',
      'HealthStatus',
      'IPlatformGateway',
      'OBSERVABILITY_CAPABILITIES',
      'OBSERVABILITY_CONTRACT_VERSION',
      'OBSERVABILITY_SCHEMA',
    ].sort();

    expect(expectedSurface).toMatchInlineSnapshot(`
      [
        "CANONICAL_OBSERVABILITY_METRICS",
        "ExecutionStats",
        "HealthStatus",
        "IPlatformGateway",
        "OBSERVABILITY_CAPABILITIES",
        "OBSERVABILITY_CONTRACT_VERSION",
        "OBSERVABILITY_SCHEMA",
      ]
    `);
  });
});
