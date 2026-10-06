import { describe, expect, it } from 'vitest';
import { CliCommandDeclSchema, CliFlagDeclSchema } from '../cli-schema.js';

describe('shared plugin CLI declaration schema', () => {
  it('accepts the SDK documented string choices and array flag declarations', () => {
    expect(CliFlagDeclSchema.safeParse({ name: 'mode', type: 'string', choices: ['full', 'fast'] }).success).toBe(true);
    expect(CliFlagDeclSchema.safeParse({ name: 'repos', type: 'array', description: 'Repository list' }).success).toBe(true);
  });

  it('rejects invalid flag names and choices on non-string flags', () => {
    expect(CliFlagDeclSchema.safeParse({ name: 'assignee_add', type: 'string' }).success).toBe(false);
    expect(CliFlagDeclSchema.safeParse({ name: 'repos', type: 'array', choices: ['one'] }).success).toBe(false);
    expect(CliFlagDeclSchema.safeParse({ name: 'run-id', type: 'string', alias: 'long' }).success).toBe(false);
  });

  it('requires the V3 command fields and validates flags with the same schema', () => {
    expect(CliCommandDeclSchema.safeParse({
      path: 'release clean install', describe: 'Install a packed release', handler: './commands/install.js#default',
      flags: [{ name: 'additional-tarballs', type: 'array' }],
    }).success).toBe(true);
    expect(CliCommandDeclSchema.safeParse({
      path: 'release clean install', describe: 'Install a packed release', handler: './commands/install.js#default',
      flags: [{ name: 'additionalTarballs', type: 'string' }],
    }).success).toBe(false);
  });
});
