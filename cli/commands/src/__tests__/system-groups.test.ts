import { describe, expect, it } from 'vitest';
import { infoGroup } from '../commands/system/groups.js';

describe('info command group', () => {
  it('does not register diag under info', () => {
    expect(infoGroup.commands.map((command) => command.name)).not.toContain('diag');
  });
});
