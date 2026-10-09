import { describe, expect, it } from 'vitest';
import type { CommandManifest } from '../types.js';
import type { ManifestV3 } from '@kb-labs/plugin-contracts';

describe('V3 manifest ownership', () => {
  it('stores a V3 manifest on the command manifest and registered command', () => {
    const v3Manifest: ManifestV3 = {
      schema: 'kb.plugin/3',
      id: '@kb-labs/test-plugin',
      version: '1.0.0',
      cli: { commands: [{ path: 'test run', describe: 'Run test', handler: 'dist/run.js' }] },
    };
    const manifest: CommandManifest = {
      manifestVersion: '1.0',
      segments: ['test', 'run'],
      id: 'run',
      group: 'test',
      describe: 'Run test',
      loader: async () => ({ run: async () => 0 }),
      v3Manifest,
    };
    expect(manifest.v3Manifest).toBe(v3Manifest);
  });

  it('allows system commands without a plugin manifest', () => {
    const manifest: CommandManifest = {
      manifestVersion: '1.0',
      segments: ['hello'],
      id: 'hello',
      group: 'system',
      describe: 'Say hello',
      loader: async () => ({ run: async () => 0 }),
    };
    expect(manifest.v3Manifest).toBeUndefined();
  });
});
