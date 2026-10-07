import { describe, expect, it } from 'vitest';
import { defineCommandFlags, defineFlags } from '../index.js';

describe('SDK CLI flag helpers', () => {
  // case: S-203.21
  it('shares manifest declarations for arrays and string choices', () => {
    const runtimeFlags = defineFlags({
      mode: { type: 'string', choices: ['fast', 'full'] },
      repos: { type: 'array', items: 'string' },
    });
    const manifestFlags = defineCommandFlags({
      mode: { type: 'string', choices: ['fast', 'full'] },
      repos: { type: 'array' },
    });

    expect(runtimeFlags.schema.mode.choices).toEqual(['fast', 'full']);
    expect(manifestFlags).toEqual([
      { name: 'mode', type: 'string', choices: ['fast', 'full'] },
      { name: 'repos', type: 'array' },
    ]);
  });
});
