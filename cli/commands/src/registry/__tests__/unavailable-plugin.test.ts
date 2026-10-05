import { describe, expect, it } from 'vitest';
import { toDiscoveryResults } from '../discover.js';
import { registerManifests } from '../register.js';
import { createRegistry } from '../service.js';

describe('unbuilt locked workspace plugins', () => {
  it('keeps the namespace in help and routes its commands to build guidance', async () => {
    const results = toDiscoveryResults({
      plugins: [],
      manifests: new Map(),
      diagnostics: [],
      failures: [{
        id: '@kb-labs/release',
        packageName: '@kb-labs/release-manager-cli',
        packageRoot: '/workspace/plugins/release/manager-cli',
        scope: 'platform',
        origin: 'linked',
        manifestPath: '/workspace/plugins/release/manager-cli/dist/manifest.js',
        reason: 'manifest',
        message: 'No manifest found; run `pnpm build`.',
        diagnosticCode: 'PLUGIN_BUILD_MISSING',
        remediation: 'Run `pnpm build` and retry the command.',
      }],
    });
    const registry = createRegistry();
    await registerManifests(results, registry);

    const route = registry.resolve(['release', 'plan']);
    expect(route.type).toBe('command');
    if (route.type === 'command') {
      expect(route.command.available).toBe(false);
      expect(route.command.hint).toContain('pnpm build');
      expect(route.command.manifest.manifestV2?.cli?.groupMeta?.[0]?.path).toBe('release');
      expect(route.command.manifest.describe).toContain('pnpm build');
    }
  });
});
