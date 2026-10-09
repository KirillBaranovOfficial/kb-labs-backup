import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  GatewayAuthHealthResponseSchema,
  GatewayBootstrapStatusSchema,
} from '../auth-health-response.js';

const contract = JSON.parse(readFileSync(new URL('../auth-health-response.schema.json', import.meta.url), 'utf8')) as {
  properties: { bootstrap: { enum: string[] } };
};
const fixture = JSON.parse(readFileSync(new URL('../__fixtures__/auth-health-response.json', import.meta.url), 'utf8')) as unknown;

describe('Gateway auth health wire contract', () => {
  it('accepts the shared response fixture and rejects undeclared fields', () => {
    expect(GatewayAuthHealthResponseSchema.parse(fixture)).toMatchObject({ bootstrap: 'provisioned' });
    expect(GatewayAuthHealthResponseSchema.safeParse({ ...(fixture as Record<string, unknown>), users: [] }).success).toBe(false);
  });

  it('keeps the TypeScript bootstrap enum aligned with the owning JSON schema', () => {
    expect([...GatewayBootstrapStatusSchema.options]).toEqual(contract.properties.bootstrap.enum);
    for (const status of contract.properties.bootstrap.enum) {
      expect(GatewayBootstrapStatusSchema.safeParse(status).success).toBe(true);
    }
    expect(GatewayBootstrapStatusSchema.safeParse('unknown').success).toBe(false);
  });
});
