import { describe, expect, it } from 'vitest';
import { readFile } from 'node:fs/promises';
import { registerBuiltinSystemCommands, registry } from '@kb-labs/cli-commands';
import { resolveProjectScope } from '../command-scope';

describe('bootstrap command scope resolution', () => {
  // case: S-124.10
  it('uses each registered system command declaration and keeps plugins project-scoped', () => {
    registerBuiltinSystemCommands();
    const commands = registry.listSystemCommands();

    expect(commands.length).toBeGreaterThan(0);
    for (const command of commands) {
      expect(resolveProjectScope({ type: 'system-cmd', cmd: command, rest: [] }, {}, [])).toBe(
        command.scope === 'platform' ? 'platform' : 'required',
      );
    }
    const pluginCommand = {
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0' as const, segments: ['demo'], id: 'demo', group: 'demo', describe: 'demo' },
      available: true,
      source: 'workspace' as const,
      shadowed: false,
    };
    expect(resolveProjectScope({ type: 'command', command: pluginCommand, rest: [] }, {}, ['demo'])).toBe('required');
    expect(resolveProjectScope({ type: 'not-found', input: [], suggestions: [] }, { help: true }, [])).toBe('platform');
    expect(resolveProjectScope({ type: 'not-found', input: [], suggestions: [] }, { version: true }, [])).toBe('platform');
    expect(resolveProjectScope({ type: 'not-found', input: ['missing'], suggestions: [] }, {}, ['missing'])).toBe('platform');
  });

  it('keeps command-name policy out of bootstrap scope resolution', async () => {
    const source = await readFile(new URL('../bootstrap.ts', import.meta.url), 'utf8');
    const resolver = await readFile(new URL('../command-scope.ts', import.meta.url), 'utf8');
    expect(source).not.toContain('usesPlatformScope');
    expect(source).toContain('resolveProjectScope');
    expect(resolver).not.toMatch(/cmdPath\[[^\]]+\]\s*===/);
    expect(resolver).not.toMatch(/(?:includes|isPlatformAuthExempt)\([^)]*["'][^"']+["']/);
  });
});
