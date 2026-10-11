import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { deriveProjectId } from '../project-id.js';
import { createProjectRegistry } from '../registry.js';
import { resolvePlatformDataPath, resolveProjectStateDir, resolveRuntimeStatePath } from '../state-dir.js';
import { toErrorEnvelope } from '@kb-labs/core-platform';

let sandbox: string;
let kbHome: string;
let project: string;

beforeAll(() => {
  sandbox = realpathSync(mkdtempSync(join(tmpdir(), 'kb-state-dir-')));
  kbHome = join(sandbox, 'home');
  project = join(sandbox, 'project');
  mkdirSync(kbHome);
  mkdirSync(join(project, '.kb'), { recursive: true });
});

afterAll(() => {
  rmSync(sandbox, { recursive: true, force: true });
});

describe('resolveProjectStateDir', () => {
  it('unregistered project: deterministic id from the canonical path under the given root', () => {
    const dir = resolveProjectStateDir(project, { root: kbHome });
    expect(dir).toBe(join(kbHome, 'state', deriveProjectId(project)));
    expect(resolveProjectStateDir(project, { root: kbHome })).toBe(dir);
  });

  it('registered project: same directory as the registry reports', async () => {
    const registry = createProjectRegistry({ root: kbHome });
    const { project: entry } = await registry.add(project);
    expect(resolveProjectStateDir(project, { root: kbHome })).toBe(registry.stateDir(entry.id));
  });

  it('respects KB_HOME from the environment and never defaults into the repository', () => {
    const dir = resolveProjectStateDir(project, { env: { KB_HOME: kbHome } });
    expect(dir.startsWith(join(kbHome, 'state') + '/')).toBe(true);
    expect(dir.startsWith(project)).toBe(false);
  });

  it('explicit root wins over KB_HOME', () => {
    const other = join(sandbox, 'other-home');
    expect(resolveProjectStateDir(project, { root: other, env: { KB_HOME: kbHome } })).toBe(
      join(other, 'state', deriveProjectId(project)),
    );
  });

  it('a symlink to the project resolves to the same directory', () => {
    const link = join(sandbox, 'project-link');
    symlinkSync(project, link);
    expect(resolveProjectStateDir(link, { root: kbHome })).toBe(resolveProjectStateDir(project, { root: kbHome }));
  });

  it('a trailing separator does not change the directory', () => {
    expect(resolveProjectStateDir(`${project}/`, { root: kbHome })).toBe(
      resolveProjectStateDir(project, { root: kbHome }),
    );
  });

  it('a not-yet-existing folder is canonicalized through its existing ancestor', () => {
    const link = join(sandbox, 'ancestor-link');
    symlinkSync(sandbox, link);
    const viaLink = resolveProjectStateDir(join(link, 'future', 'app'), { root: kbHome });
    const direct = resolveProjectStateDir(join(sandbox, 'future', 'app'), { root: kbHome });
    expect(viaLink).toBe(direct);
  });

  it('different projects get different directories', () => {
    const second = join(sandbox, 'second');
    mkdirSync(second);
    expect(resolveProjectStateDir(second, { root: kbHome })).not.toBe(resolveProjectStateDir(project, { root: kbHome }));
  });
});

describe('resolveRuntimeStatePath', () => {
  const segments = ['cache', 'cli-manifests.json'] as const;

  it('joins the segments under the project state directory', () => {
    expect(resolveRuntimeStatePath(project, segments, { root: kbHome })).toBe(
      join(resolveProjectStateDir(project, { root: kbHome }), ...segments),
    );
  });

  it('ignores files an older version left in the repository', () => {
    const legacyOnly = join(sandbox, 'legacy-only');
    mkdirSync(join(legacyOnly, '.kb', 'cache'), { recursive: true });
    writeFileSync(join(legacyOnly, '.kb', 'cache', 'cli-manifests.json'), '{}');
    const resolved = resolveRuntimeStatePath(legacyOnly, segments, { root: kbHome });
    expect(resolved.startsWith(legacyOnly)).toBe(false);
    expect(existsSync(resolved)).toBe(false);
  });
});

describe('resolvePlatformDataPath', () => {
  // case: S-054.22
  it('resolves relative paths under platform data and preserves absolute paths', () => {
    const dataRoot = '/install/state/data';
    const context = { dataRoot };
    expect(resolvePlatformDataPath('database/kb.sqlite', context, 'platform.adapterOptions.documentDatabase.filename'))
      .toBe('/install/state/data/database/kb.sqlite');
    expect(resolvePlatformDataPath('/var/lib/kb/external.sqlite', context, 'platform.adapterOptions.documentDatabase.filename'))
      .toBe('/var/lib/kb/external.sqlite');
  });

  // case: S-054.22
  it('rejects repository-prefixed and escaping relative adapter paths with corrected commands', () => {
    const optionPath = 'platform.adapterOptions.documentDatabase.filename';
    const capture = (value: string): unknown => {
      try {
        resolvePlatformDataPath(value, { dataRoot: '/install/state/data' }, optionPath);
      } catch (error) {
        return error;
      }
      throw new Error(`expected ${value} to be rejected`);
    };
    const prefixed = capture('.kb/database/kb.sqlite');
    expect(prefixed).toMatchObject({ name: 'AdapterConfigError' });
    expect(toErrorEnvelope(prefixed)).toMatchObject({
      code: 'KB_CONFIG_INVALID',
      details: { path: optionPath },
      hint: expect.stringContaining("kb config set --scope platform platform.adapterOptions.documentDatabase.filename 'database/kb.sqlite'"),
    });
    const escaping = capture('../outside.sqlite');
    expect(toErrorEnvelope(escaping)).toMatchObject({
      code: 'KB_CONFIG_INVALID',
      details: { path: optionPath },
      hint: expect.stringContaining("kb config set --scope platform platform.adapterOptions.documentDatabase.filename 'outside.sqlite'"),
    });
  });
});
