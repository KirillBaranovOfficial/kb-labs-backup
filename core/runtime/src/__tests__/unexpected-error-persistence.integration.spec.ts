import { afterAll, describe, expect, it } from 'vitest';
import { copyFile, mkdtemp, mkdir, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createEmptyLock, createMarketplaceEntry, findWorkspacePackages, writeMarketplaceLock, type MarketplaceAdapterEntry } from '@kb-labs/core-discovery';
import { ADAPTER_DEFAULTS, loadAdapterManifest } from '@kb-labs/core-platform';

const repositoryRoot = path.resolve(fileURLToPath(import.meta.url), '../../../../..');
const cliRoot = path.join(repositoryRoot, 'cli/bin');
const cliEntry = path.join(cliRoot, 'dist/bin.js');
const temporaryRoots: string[] = [];

describe('installed default log persistence across CLI processes', () => {
  afterAll(async () => {
    await Promise.all(temporaryRoots.map(root => rm(root, { recursive: true, force: true })));
  });

  it('flushes a process-level uncaught exception through the built CLI lifecycle and queries it from a second process', async () => {
    const tempRoot = await mkdtemp(path.join(tmpdir(), 'kb-log-persistence-platform-'));
    temporaryRoots.push(tempRoot);
    const home = path.join(tempRoot, 'home');
    const platformRoot = path.join(tempRoot, 'platform');
    await Promise.all([mkdir(home, { recursive: true }), mkdir(platformRoot, { recursive: true })]);
    const layoutPath = path.join(platformRoot, '.kb/v2/platform-layout.json');
    await mkdir(path.dirname(layoutPath), { recursive: true });
    await copyFile(path.join(repositoryRoot, 'tools/kb-create/v2/contracts/platform-layout.json'), layoutPath);
    const stateDataRoot = path.join(platformRoot, 'state/data');
    await mkdir(stateDataRoot, { recursive: true });
    await symlink(stateDataRoot, path.join(platformRoot, '.kb/data'), 'dir');

    const registry = JSON.parse(await readFile(path.join(repositoryRoot, 'e2e/platform/registry-manifest.json'), 'utf8')) as {
      adapters: Array<{ name: string }>;
    };
    const workspacePackages = new Map((await findWorkspacePackages(repositoryRoot))
      .map(candidate => [candidate.packageName, candidate.packageRoot]));
    const requiredSlots = Object.entries(ADAPTER_DEFAULTS)
      .filter(([, policy]) => 'preferInstalledDefault' in policy && policy.preferInstalledDefault)
      .map(([slot]) => slot);
    const defaults = new Map<string, { packageName: string; packageRoot: string; slots: string[] }>();
    for (const { name } of registry.adapters) {
      const packageRoot = workspacePackages.get(name);
      if (!packageRoot) { continue; }
      const loaded = await loadAdapterManifest({ packageRoot });
      if (loaded.kind === 'failed') { throw new Error(`${name}: ${loaded.reason}`); }
      if (loaded.kind !== 'adapter') { continue; }
      const slots = loaded.manifest.defaultFor ?? [];
      if (slots.length) { defaults.set(name, { packageName: name, packageRoot, slots }); }
    }
    const manifestSlots = [...new Set([...defaults.values()].flatMap(adapter => adapter.slots))];
    const selected = new Map<string, { packageName: string; packageRoot: string }>();
    for (const slot of new Set([...requiredSlots, ...manifestSlots])) {
      const candidates = [...defaults.values()].filter(adapter => adapter.slots.includes(slot));
      if (!candidates.length) { continue; }
      expect(candidates, `zero-config default for ${slot}`).toHaveLength(1);
      const candidate = candidates[0];
      if (candidate) { selected.set(candidate.packageName, candidate); }
    }
    const logPersistenceDefault = [...selected.values()].find(adapter =>
      defaults.get(adapter.packageName)?.slots.includes('logPersistence'));
    expect(logPersistenceDefault?.packageName).toBe('@kb-labs/adapters-log-document');

    const adapters: Record<string, MarketplaceAdapterEntry> = {};
    for (const { packageName: name, packageRoot } of selected.values()) {
      const packagePath = path.join(platformRoot, 'node_modules', name);
      await mkdir(path.dirname(packagePath), { recursive: true });
      await symlink(packageRoot, packagePath, 'dir');
      const entry = createMarketplaceEntry({
        version: '2.120.1',
        integrity: 'sha512-aW50ZWdyYXRpb24=',
        primaryKind: 'adapter',
        provides: ['adapter'],
        source: 'platform',
        package: name,
        resolvedPath: path.relative(platformRoot, packagePath),
      });
      if (entry.source !== 'platform') { throw new Error(`expected an installed adapter entry for ${name}`); }
      adapters[name] = { ...entry, enabled: true };
    }
    await writeMarketplaceLock(platformRoot, { ...createEmptyLock(), adapters });

    const env = cleanEnvironment(home, platformRoot);
    const exceptionTrigger = path.join(tempRoot, 'unexpected-exception.mjs');
    await writeFile(exceptionTrigger, `
      const originalWrite = process.stderr.write;
      let exceptionScheduled = false;
      process.stderr.write = function (...args) {
        const result = Reflect.apply(originalWrite, this, args);
        if (!exceptionScheduled && String(args[0]).includes('Adapters initialised:')) {
          exceptionScheduled = true;
          process.nextTick(() => {
            throw new Error('outer database failure', {
              cause: new Error('inner disk failure'),
            });
          });
        }
        return result;
      };
    `);
    env.KB_LOG_LEVEL = 'info';
    const produced = spawnSync(process.execPath, [
      '--import', pathToFileURL(exceptionTrigger).href,
      cliEntry, 'adapter', 'list', '--json',
    ], {
      cwd: platformRoot, env, encoding: 'utf8', timeout: 15_000,
    });
    expect(produced.status, produced.stderr || produced.stdout).toBe(1);
    expect(produced.signal).toBeNull();
    const outputEnvelopes = produced.stdout.trim().split('\n').map(line => JSON.parse(line) as {
      ok: boolean;
      error?: { code?: string; correlationId?: string; cause?: unknown; stack?: unknown };
    });
    const failure = outputEnvelopes.find(envelope => envelope.error?.code === 'KB_RUNTIME_UNEXPECTED');
    expect(failure).toBeDefined();
    if (!failure) { throw new Error('built CLI process handler did not render KB_RUNTIME_UNEXPECTED'); }
    expect(failure.ok).toBe(false);
    expect(failure.error?.code).toBe('KB_RUNTIME_UNEXPECTED');
    const correlationId = failure.error?.correlationId;
    expect(correlationId).toBeTruthy();
    if (!correlationId) { throw new Error('built CLI error envelope omitted its correlation ID'); }
    expect(failure.error).not.toHaveProperty('cause');
    expect(failure.error).not.toHaveProperty('stack');

    const queried = spawnSync(process.execPath, [cliEntry, 'logs', 'query', '--correlation-id', correlationId, '--json'], {
      cwd: platformRoot, env, encoding: 'utf8', timeout: 15_000,
    });
    expect(queried.status, queried.stderr || queried.stdout).toBe(0);
    const envelope = JSON.parse(queried.stdout) as {
      ok: boolean;
      data?: { data?: { logs?: Array<Record<string, unknown>> } };
    };
    expect(envelope.ok).toBe(true);
    expect(envelope.data?.data).toMatchObject({ source: 'persistence' });
    const record = envelope.data?.data?.logs?.find(log => log.correlationId === correlationId);
    expect(record, JSON.stringify(envelope)).toBeDefined();
    expect(record).toMatchObject({
      event: 'cli.unexpected_error',
      errorClass: 'Error',
      errorMessage: 'outer database failure',
      causeChain: [
        expect.objectContaining({ name: 'Error', message: 'outer database failure' }),
        expect.objectContaining({ name: 'Error', message: 'inner disk failure' }),
      ],
    });
    expect(record?.stack).toContain('outer database failure');
  }, 40_000);
});

function cleanEnvironment(home: string, platformRoot: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env, HOME: home, KB_PLATFORM_ROOT: platformRoot };
  delete env.KB_HOME;
  delete env.KB_NET_OFFSET;
  delete env.GOFLAGS;
  return env;
}
