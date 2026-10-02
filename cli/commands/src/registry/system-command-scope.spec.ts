import { describe, expect, it } from 'vitest';
import { registerBuiltinSystemCommands } from '../utils/register';
import { registry } from './service';

describe('system command scope declarations', () => {
  // case: S-124.10
  it('declares scope on every command in the live system registry', async () => {
    registerBuiltinSystemCommands();
    const commands = registry.listSystemCommands();

    expect(commands.length).toBeGreaterThan(0);
    expect(commands.every((command) => command.scope === 'platform' || command.scope === 'project')).toBe(true);
  });
});
