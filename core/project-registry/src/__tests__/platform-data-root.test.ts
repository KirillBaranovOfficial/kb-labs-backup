import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { resolvePlatformDataPath } from '../state-dir.js';
import { resolvePlatformDataRoot } from '../platform-data-root.js';

describe('resolvePlatformDataRoot', () => {
  let tempRoot: string | undefined;

  afterEach(async () => {
    if (tempRoot) await rm(tempRoot, { recursive: true, force: true });
    tempRoot = undefined;
  });

  // case: S-227.1
  it('uses the source layout contract for a development workspace', async () => {
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-platform-data-dev-'));
    const workspace = path.join(tempRoot, 'workspace');
    const contract = path.join(workspace, 'tools/kb-create/v2/contracts/platform-layout.json');
    await mkdir(path.dirname(contract), { recursive: true });
    await writeFile(contract, await readFile(new URL('../../../../tools/kb-create/v2/contracts/platform-layout.json', import.meta.url)));

    const dataRoot = await resolvePlatformDataRoot(workspace);
    expect(dataRoot).toBe(path.join(workspace, '.kb/data'));
    expect(resolvePlatformDataPath('database/kb.sqlite', { dataRoot }, 'platform.adapterOptions.documentDatabase.filename'))
      .toBe(path.join(workspace, '.kb/data/database/kb.sqlite'));
  });

  // case: S-054.21
  it('follows the layout projection to shared install state for an installed platform', async () => {
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-platform-data-installed-'));
    const installRoot = path.join(tempRoot, 'install');
    const versionRoot = path.join(installRoot, 'versions', 'v1');
    const activeRoot = path.join(installRoot, 'current');
    const dataRoot = path.join(installRoot, 'state', 'data');
    const contract = path.join(versionRoot, '.kb/v2/platform-layout.json');
    await mkdir(path.dirname(contract), { recursive: true });
    await mkdir(dataRoot, { recursive: true });
    await writeFile(contract, await readFile(new URL('../../../../tools/kb-create/v2/contracts/platform-layout.json', import.meta.url)));
    const projectedData = path.join(versionRoot, '.kb/data');
    await mkdir(path.dirname(projectedData), { recursive: true });
    await symlink(path.relative(path.dirname(projectedData), dataRoot), projectedData, 'dir');
    await symlink(versionRoot, activeRoot, 'dir');

    await expect(resolvePlatformDataRoot(activeRoot, false)).resolves.toBe(await realpath(dataRoot));
  });

  it('fails when an installed root has no layout contract', async () => {
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-platform-data-missing-contract-'));
    const installRoot = path.join(tempRoot, 'install');
    await mkdir(installRoot, { recursive: true });

    await expect(resolvePlatformDataRoot(installRoot, false))
      .rejects.toThrow(`Cannot read platform data-root layout contract at ${path.join(installRoot, '.kb/v2/platform-layout.json')}`);
  });
});
