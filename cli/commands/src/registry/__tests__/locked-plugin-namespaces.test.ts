import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../..');

describe('locked CLI plugin namespaces', () => {
  // case: S-142.5
  it('does not let two locked plugins claim the same first command segment', async () => {
    const lock = JSON.parse(await readFile(path.join(repoRoot, '.kb/marketplace.lock'), 'utf8')) as {
      plugins: Record<string, { link?: string; enabled?: boolean }>;
    };
    const owners = new Map<string, string>();
    for (const pluginId of ['@kb-labs/agents', '@kb-labs/host-agent']) {
      const entry = lock.plugins[pluginId];
      expect(entry?.enabled, `${pluginId} must be locked and enabled`).toBe(true);
      expect(entry?.link, `${pluginId} must have a local workspace link`).toBeTruthy();
      const manifestPath = path.join(repoRoot, entry!.link!, 'dist/manifest.js');
      const module = await import(pathToFileURL(manifestPath).href) as {
        manifest?: { cli?: { commands?: Array<{ path: string }> } };
      };
      const commands = module.manifest?.cli?.commands ?? [];
      expect(commands.length, `${pluginId} must expose commands`).toBeGreaterThan(0);
      for (const command of commands) {
        const namespace = command.path.split(/\s+/)[0]!;
        const owner = owners.get(namespace);
        expect(owner === undefined || owner === pluginId,
          `namespace "${namespace}" is also claimed by ${pluginId}`).toBe(true);
        owners.set(namespace, pluginId);
      }
    }
    expect(owners.get('workspace')).toBe('@kb-labs/host-agent');
    expect(owners.get('agent')).toBe('@kb-labs/agents');
  });
});
