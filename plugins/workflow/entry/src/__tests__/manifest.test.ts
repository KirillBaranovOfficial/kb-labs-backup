import { describe, expect, it } from 'vitest';
import { manifest } from '../manifest.js';

describe('workflow CLI manifest', () => {
  it('registers raw job inspection under the job subgroup and removes deprecated aliases', () => {
    const commands = manifest.cli?.commands ?? [];
    const paths = commands.map((command) => command.path);

    expect(paths).toContain('workflow job status');
    expect(paths).toContain('workflow job logs');
    expect(paths).toContain('workflow job list');
    expect(paths).toContain('workflow runs status');
    expect(paths).toContain('workflow runs logs');
    expect(paths).toContain('workflow runs list');
    expect(paths).not.toContain('workflow status');
    expect(paths).not.toContain('workflow logs');
    expect(paths).not.toContain('workflow list');

    const runCommand = commands.find((command) => command.path === 'workflow run');
    expect(runCommand?.flags).not.toHaveProperty('inputs');
  });
});
