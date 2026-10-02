import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { projectSettingsPath, readProjectSettings, writeProjectSetting } from '../project-settings-store.js';

const temporaryDirectories: string[] = [];

async function temporaryRoot(): Promise<string> {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-project-settings-'));
  temporaryDirectories.push(root);
  return root;
}

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((directory) => fs.rm(directory, { recursive: true, force: true })));
});

describe('platform-owned project settings store', () => {
  // case: S-204.2
  it('stores each project settings document under its ID in the platform state directory', async () => {
    const root = await temporaryRoot();
    const a = { projectId: 'prj_a', root };
    const b = { projectId: 'prj_b', root };

    await writeProjectSetting({
      ...a,
      path: ['plugins', 'commit', 'scope', 'scopes'],
      value: [{ id: 'a' }],
      settingsSchema: { type: 'object' },
    });

    expect(projectSettingsPath(a)).toBe(path.join(root, 'state', 'prj_a', 'config.jsonc'));
    expect(await readProjectSettings(a)).toEqual({ plugins: { commit: { scope: { scopes: [{ id: 'a' }] } } } });
    expect(await readProjectSettings(b)).toEqual({});
    await expect(fs.access(path.join(root, 'prj_a'))).rejects.toThrow();
  });

  // case: S-204.8
  it('never reads a same-named config file from the project repository', async () => {
    const root = await temporaryRoot();
    const repository = path.join(root, 'repository');
    await fs.mkdir(path.join(repository, '.kb'), { recursive: true });
    await fs.writeFile(path.join(repository, '.kb', 'kb.config.json'), JSON.stringify({ plugins: { commit: { legacy: true } } }));

    await writeProjectSetting({
      projectId: 'prj_repository', root, path: ['plugins', 'commit', 'enabled'], value: true,
      settingsSchema: { type: 'object', properties: { enabled: { type: 'boolean' } } },
    });

    expect(await readProjectSettings({ projectId: 'prj_repository', root })).toEqual({
      plugins: { commit: { enabled: true } },
    });
    expect(await fs.readFile(path.join(repository, '.kb', 'kb.config.json'), 'utf8'))
      .toBe(JSON.stringify({ plugins: { commit: { legacy: true } } }));
  });

  // case: S-204.5
  it('rejects invalid declared plugin settings before replacing the prior value', async () => {
    const root = await temporaryRoot();
    const options = {
      projectId: 'prj_invalid', root,
      path: ['plugins', 'commit', 'scope', 'enabled'] as const,
    };
    await writeProjectSetting({
      ...options,
      value: true,
      settingsSchema: {
        type: 'object',
        properties: { scope: { type: 'object', properties: { enabled: { type: 'boolean' } } } },
      },
    });
    const filePath = projectSettingsPath(options);
    const before = await fs.readFile(filePath, 'utf8');

    await expect(writeProjectSetting({
      ...options,
      value: 'yes',
      settingsSchema: {
        type: 'object',
        properties: { scope: { type: 'object', properties: { enabled: { type: 'boolean' } } } },
      },
    })).rejects.toMatchObject({
      code: 'KB_PLUGIN_SETTINGS_INVALID',
      details: { path: 'plugins.commit.scope.enabled' },
    });
    expect(await fs.readFile(filePath, 'utf8')).toBe(before);
  });

  it('refuses to write plugin settings when no manifest schema is available', async () => {
    const root = await temporaryRoot();
    const options = {
      projectId: 'prj_no_schema', root,
      path: ['plugins', 'commit', 'enabled'] as const,
      value: false,
    };

    await expect(writeProjectSetting(options)).rejects.toMatchObject({
      code: 'KB_PLUGIN_SETTINGS_SCHEMA_INVALID',
    });
    await expect(fs.access(projectSettingsPath(options))).rejects.toMatchObject({ code: 'ENOENT' });
  });
});
