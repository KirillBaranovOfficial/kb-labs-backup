import { spawnSync } from 'node:child_process';
import { access, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

describe('installed platform adapter defaults', () => {
  let tempRoot: string | undefined;

  afterEach(async () => {
    if (tempRoot) await rm(tempRoot, { recursive: true, force: true });
    tempRoot = undefined;
  });

  // case: S-054.12, S-054.17
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
    const releaseConfig = await readFile(configPath, 'utf8');
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

    const command = spawnSync(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'adapter', 'list', '--json'], {
      cwd: projectRoot,
      env,
      encoding: 'utf8',
      timeout: 30_000,
    });

    expect(command.status, command.stderr || command.stdout).toBe(0);
    const payload = JSON.parse(command.stdout) as { adapters?: Array<{ slot: string; mode: string; implementation: string; baseDir?: string }> };
    expect(payload.adapters?.find(row => row.slot === 'kvStore')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-sqlite/kv',
    });
    const storage = payload.adapters?.find(row => row.slot === 'storage');
    expect(storage).toMatchObject({ mode: 'real', implementation: '@kb-labs/adapters-fs' });
    const stateRoot = path.join(env.KB_HOME!, 'state');
    expect(path.relative(stateRoot, storage?.baseDir ?? '')).toMatch(/^prj_[0-9a-f]{16}\/storage$/);
    expect(path.relative(projectRoot, storage?.baseDir ?? '').startsWith('..')).toBe(true);
    expect(payload.adapters?.find(row => row.slot === 'llm')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-openai',
      credentialsStatus: 'missing', missingCredentials: ['OPENAI_API_KEY'],
    });
  });
});
