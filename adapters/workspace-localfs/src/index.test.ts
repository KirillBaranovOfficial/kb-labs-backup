import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { access, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { resolveRuntimeStatePath } from '@kb-labs/sdk/adapters';
import { LocalFsWorkspaceAdapter } from './index.js';

let tmp: string;
let kbHome: string;
let previousKbHome: string | undefined;

beforeEach(async () => {
  tmp = await mkdtemp(path.join(os.tmpdir(), 'kb-workspace-'));
  kbHome = await mkdtemp(path.join(os.tmpdir(), 'kb-workspace-home-'));
  previousKbHome = process.env.KB_HOME;
  process.env.KB_HOME = kbHome;
});

afterEach(async () => {
  if (previousKbHome === undefined) {
    delete process.env.KB_HOME;
  } else {
    process.env.KB_HOME = previousKbHome;
  }
  await rm(tmp, { recursive: true, force: true });
  await rm(kbHome, { recursive: true, force: true });
});

async function exists(p: string): Promise<boolean> {
  return access(p).then(() => true, () => false);
}

describe('LocalFsWorkspaceAdapter', () => {
  // case: S-211.5
  it('preserves workspace registry parse failures instead of reporting a missing workspace', async () => {
    const registryDir = path.join(tmp, 'registry');
    await mkdir(registryDir, { recursive: true });
    await writeFile(path.join(registryDir, 'ws_broken.json'), '{', 'utf8');
    const adapter = new LocalFsWorkspaceAdapter({ workspace: { cwd: tmp }, registryDir });

    await expect(adapter.getStatus('ws_broken')).rejects.toThrow(SyntaxError);
  });

  it('materializes and attaches workspace', async () => {
    const adapter = new LocalFsWorkspaceAdapter({ workspace: { cwd: tmp } });
    const workspace = await adapter.materialize({ basePath: './demo' });
    expect(workspace.status).toBe('ready');

    const attachment = await adapter.attach({
      workspaceId: workspace.workspaceId,
      environmentId: 'env_1',
    });
    expect(attachment.mountPath).toBe('/workspace');

    const status = await adapter.getStatus(workspace.workspaceId);
    expect(status.status).toBe('attached');
  });

  it('writes the default workspace registry to the project state directory, not the repository', async () => {
    const adapter = new LocalFsWorkspaceAdapter({ workspace: { cwd: tmp } });
    const workspace = await adapter.materialize({ workspaceId: 'ws_default' });

    expect(workspace.rootPath).toBe(resolveRuntimeStatePath(tmp, ['runtime', 'workspaces', 'ws_default']));
    expect(await exists(path.join(resolveRuntimeStatePath(tmp, ['runtime', 'workspace-registry']), 'ws_default.json'))).toBe(true);
    expect(await exists(path.join(tmp, '.kb'))).toBe(false);
  });

  it('keeps explicit directories relative to the project', async () => {
    const adapter = new LocalFsWorkspaceAdapter({
      workspace: { cwd: tmp },
      rootDir: 'ws-root',
      registryDir: 'ws-registry',
    });
    await adapter.materialize({ workspaceId: 'ws_explicit' });

    expect(await exists(path.join(tmp, 'ws-root', 'ws_explicit'))).toBe(true);
    expect(await exists(path.join(tmp, 'ws-registry', 'ws_explicit.json'))).toBe(true);
    expect(await exists(resolveRuntimeStatePath(tmp, ['runtime']))).toBe(false);
  });
});
