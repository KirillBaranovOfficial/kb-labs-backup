import { describe, expect, it } from 'vitest';
import { createRegistry, type SystemCommand } from '../index.js';

function systemCommand(name: string, authProtection?: 'public' | 'session'): SystemCommand {
  return {
    name,
    scope: 'platform',
    authProtection,
    jsonTest: { safeArgs: [] },
    describe: `${name} command`,
    run: async () => 0,
  };
}

describe('registry protection classes', () => {
  it('derives each command class from its registered route and command declaration', () => {
    const registry = createRegistry();
    registry.register(systemCommand('login', 'public'));
    registry.register(systemCommand('settings'));
    registry.registerManifest({
      manifest: {
        manifestVersion: '1.0',
        segments: ['workflow', 'run'],
        id: 'run',
        group: 'workflow',
        describe: 'Run a workflow',
      },
      available: true,
      source: 'workspace',
      shadowed: false,
      packageName: '@kb-labs/workflow',
    });

    expect(registry.listProtectionCommands()).toEqual(expect.arrayContaining([
      { path: ['login'], protectionClass: 'public:system', requiresSession: false, available: true },
      { path: ['settings'], protectionClass: 'session:system', requiresSession: true, available: true },
      { path: ['workflow', 'run'], protectionClass: 'session:plugin', requiresSession: true, available: true },
    ]));
  });
});
