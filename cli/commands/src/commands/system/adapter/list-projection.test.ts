import { describe, expect, it } from 'vitest';
import type { AdapterManifest } from '@kb-labs/core-platform';
import { projectNetworkRequirement } from './list-projection.js';

describe('adapter list manifest projection', () => {
  it('uses the manifest declaration regardless of package naming', () => {
    const localAdapter = {
      schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'private-storage', name: 'Private Storage',
      version: '1.0.0', type: 'core', implements: 'IStorage', networkRequired: false,
    } satisfies AdapterManifest;
    const remoteAdapter = {
      ...localAdapter, id: 'private-remote-store', networkRequired: true,
    } satisfies AdapterManifest;

    expect(projectNetworkRequirement(localAdapter)).toBe(false);
    expect(projectNetworkRequirement(remoteAdapter)).toBe(true);
    expect(projectNetworkRequirement(undefined)).toBe(false);
  });
});
