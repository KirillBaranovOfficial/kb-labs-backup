import { describe, expect, it } from 'vitest';
import { manifest } from '../src/manifest.v3.js';

describe('plugin template CLI manifest', () => {
  it('declares a runnable path for every command', () => {
    expect(manifest.cli.commands.length).toBeGreaterThan(0);
    for (const command of manifest.cli.commands) {
      expect(command.path, command.id).toEqual(expect.any(String));
      expect(command.path.trim(), command.id).not.toBe('');
    }
  });
});
