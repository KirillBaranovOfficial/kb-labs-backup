import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { createProjectRegistry } from '@kb-labs/core-project-registry';
import { findWorkspaceRoot } from '../../src/core/workspace.js';

let sandbox: string;
const previousKbHome = process.env.KB_HOME;

afterEach(() => {
  if (previousKbHome === undefined) {
    delete process.env.KB_HOME;
  } else {
    process.env.KB_HOME = previousKbHome;
  }
  if (sandbox) {
    rmSync(sandbox, { recursive: true, force: true });
  }
});

describe('impact workspace root', () => {
  // case: S-204.7
  it('uses registry membership and ignores an unregistered .kb config', async () => {
    sandbox = mkdtempSync(join(tmpdir(), 'kb-impact-workspace-'));
    const home = join(sandbox, 'home');
    const registered = join(sandbox, 'registered');
    const unregistered = join(sandbox, 'unregistered');
    const nested = join(registered, 'packages', 'a');
    mkdirSync(nested, { recursive: true });
    mkdirSync(join(unregistered, '.kb'), { recursive: true });
    writeFileSync(join(unregistered, '.kb', 'kb.config.json'), '{}');
    process.env.KB_HOME = home;
    await createProjectRegistry({ root: home }).add(registered);

    expect(findWorkspaceRoot(nested)).toBe(realpathSync(registered));
    expect(() => findWorkspaceRoot(unregistered)).toThrow('kb project add');
  });
});
