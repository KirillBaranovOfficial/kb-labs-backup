import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { access, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterEach, describe, expect, it } from 'vitest';
import { createProjectRegistry } from '@kb-labs/core-project-registry';

describe('installed platform adapter defaults', () => {
  let tempRoot: string | undefined;
  let gatewayServer: Server | undefined;
  let primaryGatewayServer: Server | undefined;

  afterEach(async () => {
    if (gatewayServer?.listening) await new Promise<void>((resolve, reject) => gatewayServer!.close((error) => error ? reject(error) : resolve()));
    gatewayServer = undefined;
    if (primaryGatewayServer?.listening) await new Promise<void>((resolve, reject) => primaryGatewayServer!.close((error) => error ? reject(error) : resolve()));
    primaryGatewayServer = undefined;
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

  // case: S-054.12, S-054.17, PC-001.12
  // case: S-203.1
  it('runs adapter list with the sealed release config and installed adapter lock', async () => {
    const workspaceRoot = path.resolve(process.cwd(), '../..');
    tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-installed-defaults-'));
    const platformRoot = path.join(tempRoot, 'platform');
    const primaryPlatformRoot = path.join(tempRoot, 'primary-platform');
    const projectRoot = path.join(tempRoot, 'project');
    const home = path.join(tempRoot, 'home');
    await Promise.all([
      mkdir(path.join(platformRoot, '.kb'), { recursive: true }),
      mkdir(path.join(primaryPlatformRoot, '.kb', 'v2'), { recursive: true }),
      mkdir(projectRoot, { recursive: true }),
      mkdir(home, { recursive: true }),
    ]);

    primaryGatewayServer = createServer((request, response) => {
      response.setHeader('content-type', 'application/json');
      if (request.url === '/health/identity') {
        response.end(JSON.stringify({ ok: true, data: { installationId: 'installed-root-a' } }));
        return;
      }
      if (request.url === '/health/adapters') {
        response.end(JSON.stringify({ ok: true, data: [
          { slot: 'storage', mode: 'real', implementation: '@kb-labs/adapters-primary' },
        ] }));
        return;
      }
      response.statusCode = 404;
      response.end('{}');
    });
    await new Promise<void>((resolve) => primaryGatewayServer!.listen(0, '127.0.0.1', resolve));
    const primaryGatewayPort = (primaryGatewayServer.address() as AddressInfo).port;
    await writeFile(path.join(primaryPlatformRoot, '.kb', 'v2', 'receipt.json'), JSON.stringify({
      installationId: 'installed-root-a', verification: { hostHealthUrl: `http://127.0.0.1:${primaryGatewayPort}/health` },
    }));

    gatewayServer = createServer((request, response) => {
      response.setHeader('content-type', 'application/json');
      if (request.url === '/health/identity') {
        response.end(JSON.stringify({ ok: true, data: { installationId: 'installed-root-b' } }));
        return;
      }
      if (request.url === '/health/adapters') {
        response.end(JSON.stringify({ ok: true, data: [
          { slot: 'storage', mode: 'real', implementation: '@kb-labs/adapters-fs' },
          { slot: 'kvStore', mode: 'real', implementation: '@kb-labs/adapters-sqlite/kv' },
          { slot: 'llm', mode: 'real', implementation: '@kb-labs/adapters-openai' },
        ] }));
        return;
      }
      response.statusCode = 404;
      response.end('{}');
    });
    await new Promise<void>((resolve) => gatewayServer!.listen(0, '127.0.0.1', resolve));
    const gatewayPort = (gatewayServer.address() as AddressInfo).port;
    await mkdir(path.join(platformRoot, '.kb', 'v2'), { recursive: true });
    await writeFile(path.join(platformRoot, '.kb', 'v2', 'receipt.json'), JSON.stringify({
      installationId: 'installed-root-b', verification: { hostHealthUrl: `http://127.0.0.1:${gatewayPort}/health` },
    }));

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
    delete env.KB_GATEWAY_URL;
    delete env.GATEWAY_PORT;
    delete env.GATEWAY_HOST;
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

    const command = await spawnCli(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'adapter', 'list', '--json'], {
      cwd: projectRoot,
      env,
    });

    expect(command.status, command.stderr || command.stdout).toBe(0);
    const payload = JSON.parse(command.stdout) as { ok?: boolean; data?: { adapters?: Array<{ slot: string; mode: string; implementation: string; baseDir?: string }> } };
    expect(payload.ok).toBe(true);
    expect(payload.data?.adapters?.find(row => row.slot === 'kvStore')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-sqlite/kv',
    });
    const storage = payload.data?.adapters?.find(row => row.slot === 'storage');
    expect(storage).toMatchObject({ mode: 'real', implementation: '@kb-labs/adapters-fs' });
    expect(storage?.implementation).not.toBe('@kb-labs/adapters-primary');
    const stateRoot = path.join(env.KB_HOME!, 'state');
    expect(path.relative(stateRoot, storage?.baseDir ?? '')).toMatch(/^prj_[0-9a-f]{16}\/storage$/);
    expect(path.relative(projectRoot, storage?.baseDir ?? '').startsWith('..')).toBe(true);
    expect(payload.data?.adapters?.find(row => row.slot === 'llm')).toMatchObject({
      mode: 'real', implementation: '@kb-labs/adapters-openai',
      credentialsStatus: 'missing', missingCredentials: ['OPENAI_API_KEY'],
    });

    await new Promise<void>((resolve, reject) => gatewayServer!.close((error) => error ? reject(error) : resolve()));
    gatewayServer = undefined;
    const stopped = await spawnCli(process.execPath, [path.join(workspaceRoot, 'cli/bin/dist/bin.js'), 'adapter', 'list', '--json'], {
      cwd: projectRoot,
      env,
    });
    expect(stopped.status, stopped.stderr || stopped.stdout).toBe(0);
    const stoppedPayload = JSON.parse(stopped.stdout) as {
      data?: { adapters?: Array<{ slot: string; runtimeStatus?: string; action?: { command: string } }> };
    };
    expect(stoppedPayload.data?.adapters?.find(row => row.slot === 'storage')).toMatchObject({
      runtimeStatus: 'unknown',
      action: { command: `kb-create start --platform-root ${JSON.stringify(platformRoot)}` },
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

function spawnCli(command: string, args: string[], options: { cwd: string; env: NodeJS.ProcessEnv }): Promise<{ status: number | null; stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, options);
    let stdout = '';
    let stderr = '';
    const timeout = setTimeout(() => child.kill('SIGTERM'), 30_000);
    child.stdout.setEncoding('utf8').on('data', (chunk: string) => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', (chunk: string) => { stderr += chunk; });
    child.once('error', reject);
    child.once('close', (status) => {
      clearTimeout(timeout);
      resolve({ status, stdout, stderr });
    });
  });
}
