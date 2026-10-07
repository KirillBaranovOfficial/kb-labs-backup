import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { access, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { createProjectRegistry } from '@kb-labs/core-project-registry';

describe('installed platform adapter defaults', () => {
  let tempRoot: string | undefined;

  afterEach(async () => {
    if (tempRoot) await rm(tempRoot, { recursive: true, force: true });
    tempRoot = undefined;
  });

  // case: S-124.1, S-124.9
  it('manages the project registry from an unregistered working directory', async () => {
    const workspaceRoot = path.resolve(process.cwd(), '../..');
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-platform-scope-'));
    const platformRoot = path.join(tempRoot, 'platform');
    const projectRoot = path.join(tempRoot, 'unregistered-project');
    const home = path.join(tempRoot, 'home');
    await Promise.all([
      mkdir(path.join(platformRoot, '.kb'), { recursive: true }),
      mkdir(projectRoot, { recursive: true }),
      mkdir(home, { recursive: true }),
    ]);
    await writeFile(path.join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({ platform: { adapters: {} } }));

    const result = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'project', 'list', '--json'], {
      cwd: projectRoot,
      env: { ...process.env, HOME: home, KB_HOME: home, KB_PLATFORM_ROOT: platformRoot },
      encoding: 'utf8',
      timeout: 30_000,
    });
    expect(result.status, result.stderr || result.stdout).toBe(0);
    const payload: unknown = JSON.parse(result.stdout);
    const findProjects = (value: unknown): unknown[] | undefined => {
      if (Array.isArray(value)) return value.flatMap((item) => {
        const found = findProjects(item);
        return found === undefined ? [] : [found];
      })[0] as unknown[] | undefined;
      if (value && typeof value === 'object') {
        const record = value as Record<string, unknown>;
        if (Array.isArray(record.projects)) return record.projects;
        for (const child of Object.values(record)) {
          const found = findProjects(child);
          if (found !== undefined) return found;
        }
      }
      return undefined;
    };
    expect(findProjects(payload)).toEqual([]);

    const target = path.join(tempRoot, 'registered-project');
    await mkdir(target);
    const add = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'project', 'add', target, '--json'], {
      cwd: projectRoot,
      env: { ...process.env, HOME: home, KB_HOME: home, KB_PLATFORM_ROOT: platformRoot },
      encoding: 'utf8',
      timeout: 30_000,
    });
    expect(add.status, add.stderr || add.stdout).toBe(0);
    expect(add.stdout).toContain(path.resolve(target));
    await expect(access(path.join(target, '.kb'))).rejects.toThrow();
    expect(await readdir(projectRoot)).toEqual([]);
  });

  // case: S-054.12, S-054.17
  // case: S-203.1
  it('runs adapter list with the sealed release config and installed adapter lock', async () => {
    const workspaceRoot = path.resolve(process.cwd(), '../..');
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-installed-defaults-'));
    const platformRoot = path.join(tempRoot, 'platform');
    const projectRoot = path.join(tempRoot, 'project');
    const home = path.join(tempRoot, 'home');
    await Promise.all([
      mkdir(path.join(platformRoot, '.kb'), { recursive: true }),
      mkdir(projectRoot, { recursive: true }),
      mkdir(home, { recursive: true }),
    ]);

    const configPath = path.join(workspaceRoot, '.kb', 'kb.config.jsonc');
    const workspaceConfig = await readFile(configPath, 'utf8');
    const releaseConfig = workspaceConfig.replace(/"mode":\s*"development",\s*/, '');
    await writeFile(path.join(platformRoot, '.kb', 'kb.config.jsonc'), releaseConfig);

    const config = JSON.parse(releaseConfig.replace(/,\s*([}\]])/g, '$1')) as {
      platform: { adapters: Record<string, string>; adapterOptions?: Record<string, unknown> };
    };
    delete config.platform.adapters.storage;
    if (config.platform.adapterOptions) delete config.platform.adapterOptions.storage;
    await writeFile(path.join(platformRoot, '.kb', 'kb.config.jsonc'), `${JSON.stringify(config, null, 2)}\n`);
    const packages = [...new Set([
      ...Object.values(config.platform.adapters).map(value => value.split('/').slice(0, 2).join('/')),
      '@kb-labs/adapters-openai',
      '@kb-labs/adapters-fs',
    ])];
    const adapters = Object.fromEntries(packages.map(packageName => {
      const packagePath = path.join(workspaceRoot, 'adapters', packageName.replace('@kb-labs/adapters-', ''));
      return [packageName, {
        version: '2.120.1',
        integrity: 'sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
        resolvedPath: path.relative(platformRoot, packagePath),
        source: 'platform',
        enabled: true,
      }];
    }));
    await writeFile(path.join(platformRoot, '.kb', 'marketplace.lock'), JSON.stringify({
      schema: 'kb.marketplace/3', plugins: {}, adapters,
    }));

    const env: NodeJS.ProcessEnv = { ...process.env, HOME: home, KB_HOME: path.join(home, '.kb'), KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot };
    delete env.KB_NET_OFFSET;
    const help = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), '--help'], {
      cwd: projectRoot,
      env,
      encoding: 'utf8',
      timeout: 30_000,
    });
    expect(help.status, help.stderr || help.stdout).toBe(0);
    expect(help.stdout).toContain('KB Labs CLI');
    expect(await readdir(projectRoot)).toEqual([]);
    await expect(access(path.join(projectRoot, '.kb'))).rejects.toThrow();
    await expect(access(path.join(env.KB_HOME!, 'state'))).rejects.toThrow();

    const version = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), '--version'], {
      cwd: projectRoot,
      env,
      encoding: 'utf8',
      timeout: 30_000,
    });
    expect(version.status, version.stderr || version.stdout).toBe(0);
    expect(version.stdout.trim()).not.toBe('');
    await expect(access(path.join(projectRoot, '.kb'))).rejects.toThrow();
    await expect(access(path.join(env.KB_HOME!, 'state'))).rejects.toThrow();

    await createProjectRegistry({ env }).add(projectRoot);

    const command = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'adapter', 'list', '--json'], {
      cwd: projectRoot,
      env,
      encoding: 'utf8',
      timeout: 30_000,
    });

    expect(command.status, command.stderr || command.stdout).toBe(0);
    const payload = JSON.parse(command.stdout) as { ok?: boolean; data?: { adapters?: Array<{ slot: string; mode: string; implementation: string; baseDir?: string }> } };
    expect(payload.ok).toBe(true);
    expect(payload.data?.adapters?.find(row => row.slot === 'kvStore')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-sqlite/kv',
    });
    const storage = payload.data?.adapters?.find(row => row.slot === 'storage');
    expect(storage).toMatchObject({ mode: 'real', implementation: '@kb-labs/adapters-fs' });
    const stateRoot = path.join(env.KB_HOME!, 'state');
    expect(path.relative(stateRoot, storage?.baseDir ?? '')).toMatch(/^prj_[0-9a-f]{16}\/storage$/);
    expect(path.relative(projectRoot, storage?.baseDir ?? '').startsWith('..')).toBe(true);
    expect(payload.data?.adapters?.find(row => row.slot === 'llm')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-openai',
      credentialsStatus: 'missing', missingCredentials: ['OPENAI_API_KEY'],
    });
  });

  // case: S-203.1
  it('keeps discovery diagnostics off JSON stdout', async () => {
    const workspaceRoot = path.resolve(process.cwd(), '../..');
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-json-discovery-diagnostic-'));
    const platformRoot = path.join(tempRoot, 'platform');
    const projectRoot = path.join(tempRoot, 'project');
    const home = path.join(tempRoot, 'home');
    await Promise.all([mkdir(projectRoot), mkdir(home)]);
    await mkdir(path.join(platformRoot, '.kb'), { recursive: true });
    await mkdir(path.join(projectRoot, '.kb'));
    const workspaceConfig = await readFile(path.join(workspaceRoot, '.kb', 'kb.config.jsonc'), 'utf8');
    const releaseConfig = workspaceConfig.replace(/"mode":\s*"development",\s*/, '');
    const config = JSON.parse(releaseConfig.replace(/,\s*([}\]])/g, '$1')) as {
      platform: { adapters: Record<string, string>; adapterOptions?: Record<string, unknown> };
    };
    delete config.platform.adapters.storage;
    if (config.platform.adapterOptions) delete config.platform.adapterOptions.storage;
    await writeFile(path.join(platformRoot, '.kb', 'kb.config.jsonc'), `${JSON.stringify(config, null, 2)}\n`);
    const packages = [...new Set([
      ...Object.values(config.platform.adapters).map(value => value.split('/').slice(0, 2).join('/')),
      '@kb-labs/adapters-openai',
      '@kb-labs/adapters-fs',
    ])];
    const adapters = Object.fromEntries(packages.map(packageName => {
      const packagePath = path.join(workspaceRoot, 'adapters', packageName.replace('@kb-labs/adapters-', ''));
      return [packageName, {
        version: '2.120.1',
        integrity: 'sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
        resolvedPath: path.relative(platformRoot, packagePath),
        source: 'platform',
        enabled: true,
      }];
    }));
    const pluginRoot = path.join(workspaceRoot, 'plugins/marketplace/entry');
    const pluginPackageJSON = await readFile(path.join(pluginRoot, 'package.json'));
    await writeFile(path.join(platformRoot, '.kb', 'marketplace.lock'), JSON.stringify({
      schema: 'kb.marketplace/3',
      plugins: {
        marketplace: {
          version: '2.120.1',
          integrity: `sha256-${createHash('sha256').update(pluginPackageJSON).digest('base64')}`,
          package: '@kb-labs/marketplace-entry',
          resolvedPath: path.relative(platformRoot, pluginRoot),
          source: 'platform',
          enabled: true,
        },
      },
      adapters,
    }));

    const env: NodeJS.ProcessEnv = { ...process.env, HOME: home, KB_HOME: path.join(home, '.kb'), KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot };
    delete env.KB_NET_OFFSET;
    await createProjectRegistry({ env }).add(projectRoot);
    const command = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'project', 'list', '--json'], {
      cwd: projectRoot,
      env,
      encoding: 'utf8',
      timeout: 30_000,
    });
    expect(command.status, command.stderr || command.stdout).toBe(0);
    expect(JSON.parse(command.stdout)).toMatchObject({ ok: true, data: expect.anything() });
    expect(command.stdout).not.toContain('[discover]');
    expect(command.stderr).toContain('PLUGIN_ID_MISMATCH');
  });
});
