import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { noopTraceContext, noopUI } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import { createProjectRegistry } from '@kb-labs/core-project-registry';
import { writeMarketplaceLock } from '@kb-labs/core-discovery';
import { adapterBind } from '../../commands/system/adapter/bind.js';
import { adapterList } from '../../commands/system/adapter/list.js';
import { adapterUnbind } from '../../commands/system/adapter/unbind.js';
import { configSet } from '../../commands/system/config/set.js';

vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  resolveActiveGateway: async ({ env }: { env: NodeJS.ProcessEnv }) => ({
    gatewayUrl: env.KB_GATEWAY_URL ?? 'http://127.0.0.1:4000',
  }),
}));

let sandbox: string;
let platformRoot: string;
let projectRoot: string;

function context() {
  const outputs: unknown[] = [];
  const errors: string[] = [];
  const hints: string[] = [];
  const ctx: PluginContextV3 = {
    host: 'cli',
    requestId: 'adapter-bind-test',
    pluginId: '@kb-labs/system',
    pluginVersion: '1.0.0',
    cwd: projectRoot,
    ui: {
      ...noopUI,
      json: (data: unknown) => outputs.push(data),
      error: (message: string, options?: { hint?: string }) => {
        errors.push(message);
        if (options?.hint) hints.push(options.hint);
      },
    },
    platform: { logger: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn(), child: vi.fn().mockReturnThis() } } as never,
    runtime: { fs: {} as never, fetch: vi.fn(), env: vi.fn() },
    api: {} as never,
    hostContext: { host: 'cli', argv: [], flags: {} },
    trace: noopTraceContext,
  };
  return { ctx, outputs, errors, hints };
}

function dataOf<T>(envelope: unknown): T {
  expect(envelope).toMatchObject({ ok: true, data: expect.anything() });
  return (envelope as { data: T }).data;
}

function mockAdapterHealth(ctx: PluginContextV3, statuses: Array<Record<string, unknown>>): void {
  vi.spyOn(ctx.runtime, 'fetch').mockResolvedValue(new Response(JSON.stringify(statuses), { status: 200 }));
}

beforeEach(async () => {
  sandbox = await mkdtemp(join(tmpdir(), 'kb-adapter-bind-'));
  const kbHome = join(sandbox, 'kb-home');
  platformRoot = join(sandbox, 'platform');
  projectRoot = join(sandbox, 'project');
  await mkdir(join(platformRoot, '.kb'), { recursive: true });
  await mkdir(join(projectRoot, '.kb'), { recursive: true });
  projectRoot = await realpath(projectRoot);
  await writeFile(join(platformRoot, '.kb', 'kb.config.json'), '{}');
  await writeFile(join(projectRoot, '.kb', 'kb.config.json'), '{}');
  vi.stubEnv('KB_PLATFORM_ROOT', platformRoot);
  vi.stubEnv('KB_HOME', kbHome);
  await createProjectRegistry({ env: process.env }).add(projectRoot);
});

afterEach(async () => {
  vi.unstubAllEnvs();
  await rm(sandbox, { recursive: true, force: true });
});

describe('kb adapter list', () => {
  // case: S-211.6
  it('reports a runtime adapter load failure with its recovery command', async () => {
    const { ctx, outputs } = context();
    vi.stubEnv('KB_GATEWAY_URL', 'http://127.0.0.1:4000');
    vi.spyOn(ctx.runtime, 'fetch').mockResolvedValue(new Response(JSON.stringify([
      {
        slot: 'llm',
        mode: 'degraded',
        implementation: '@kb-labs/adapters-vibeproxy',
        configuredPackage: '@kb-labs/adapters-vibeproxy',
        reason: 'load-failed',
        correlationId: 'load-123',
        action: {
          id: 'install-adapter',
          label: 'Install @kb-labs/adapters-vibeproxy',
          command: 'kb marketplace install @kb-labs/adapters-vibeproxy --scope platform',
        },
        recordedAt: '2026-10-06T00:00:00.000Z',
      },
    ]), { status: 200 }));

    const code = await adapterList.run(ctx, [], { json: true });
    expect(code).toBe(0);
    const adapters = dataOf<{ adapters: Array<Record<string, unknown>> }>(outputs[0]).adapters;
    expect(adapters.find(({ slot }) => slot === 'llm')).toMatchObject({
      implementation: '@kb-labs/adapters-vibeproxy',
      mode: 'degraded',
      reason: 'load-failed',
      correlationId: 'load-123',
      action: { command: 'kb marketplace install @kb-labs/adapters-vibeproxy --scope platform' },
    });
  });

  // case: S-211.6
  it('reports runtime status as unknown when the resolved local host is not running', async () => {
    const { ctx, outputs } = context();
    vi.spyOn(ctx.runtime, 'env').mockReturnValue(undefined);
    const fetch = vi.spyOn(ctx.runtime, 'fetch').mockRejectedValue(new Error('connection refused'));

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    expect(fetch).toHaveBeenCalledWith('http://127.0.0.1:4000/health/adapters');
    const adapters = dataOf<{ adapters: Array<Record<string, unknown>> }>(outputs[0]).adapters;
    expect(adapters.find(({ slot }) => slot === 'llm')).toMatchObject({
      mode: 'unknown',
      runtimeStatus: 'unknown',
      action: { command: `kb-create start --platform-root "${platformRoot}"` },
    });
    expect(adapters.find(({ slot }) => slot === 'llm')?.mode).not.toBe('real');
  });

  it('shows installed platform adapters for the current project', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({
      platform: { adapters: {
        storage: '@kb-labs/adapters-fs@2.120.1',
        cache: '@kb-labs/adapters-state-broker',
        documentDatabase: '@kb-labs/adapters-sqlite',
        kvStore: '@kb-labs/adapters-sqlite/kv',
        logRingBuffer: '@kb-labs/adapters-log-ringbuffer',
        logPersistence: '@kb-labs/adapters-log-document',
      }, adapterOptions: { storage: { baseDir: '.kb/data' } } },
    }));
    const { ctx, outputs } = context();
    mockAdapterHealth(ctx, [
      { slot: 'storage', mode: 'real', implementation: '@kb-labs/adapters-fs', configuredPackage: '@kb-labs/adapters-fs' },
      { slot: 'llm', mode: 'noop', implementation: 'NoOpLLM' },
      { slot: 'cache', mode: 'real', implementation: '@kb-labs/adapters-state-broker', configuredPackage: '@kb-labs/adapters-state-broker' },
      { slot: 'vectorStore', mode: 'noop', implementation: 'unavailable' },
      { slot: 'logs', mode: 'real', implementation: 'HybridLogReader' },
      { slot: 'documentDatabase', mode: 'real', implementation: '@kb-labs/adapters-sqlite', configuredPackage: '@kb-labs/adapters-sqlite' },
      { slot: 'kvStore', mode: 'real', implementation: '@kb-labs/adapters-sqlite/kv', configuredPackage: '@kb-labs/adapters-sqlite' },
    ]);

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true });
    const adapters = dataOf<{ adapters: Array<Record<string, unknown>> }>(outputs[0]).adapters;
    expect(adapters.find(({ slot }) => slot === 'storage')).toMatchObject({
      implementation: '@kb-labs/adapters-fs', mode: 'real', baseDir: join(projectRoot, '.kb', 'data'), networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'llm')).toMatchObject({
      mode: 'unconfigured', requiresConfiguration: true, networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'cache')).toMatchObject({
      implementation: '@kb-labs/adapters-state-broker', mode: 'real', networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'vectorStore')).toMatchObject({
      implementation: null, mode: 'unconfigured', requiresConfiguration: true, networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'logs')).toMatchObject({
      implementation: 'HybridLogReader', mode: 'real', networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'documentDatabase')).toMatchObject({
      implementation: '@kb-labs/adapters-sqlite', mode: 'real', networkRequired: false,
    });
    expect(adapters.find(({ slot }) => slot === 'kvStore')).toMatchObject({
      implementation: '@kb-labs/adapters-sqlite/kv', mode: 'real', networkRequired: false,
    });
  });

  it('shows a manifest-discovered local default when an installed layout leaves the slot unbound', async () => {
    const packageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-fs');
    await mkdir(join(packageRoot, 'dist'), { recursive: true });
    await writeFile(join(packageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-fs', main: 'dist/index.js' }));
    await writeFile(join(packageRoot, 'dist', 'index.js'), [
      'export const manifest = { id: "fs-storage", defaultFor: ["storage"] };',
      'export function createAdapter() { return {}; }',
    ].join('\n'));
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3',
      plugins: {},
      adapters: {
        'fs-storage': {
          version: '1.0.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-fs',
          enabled: true, source: 'platform',
        },
      },
    });
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({
      platform: { adapterOptions: { storage: { baseDir: '.kb/data' } } },
    }));
    const { ctx, outputs } = context();
    mockAdapterHealth(ctx, [
      { slot: 'storage', mode: 'real', implementation: '@kb-labs/adapters-fs', configuredPackage: '@kb-labs/adapters-fs' },
    ]);

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    const adapters = dataOf<{ adapters: Array<Record<string, unknown>> }>(outputs[0]).adapters;
    expect(adapters.find(({ slot }) => slot === 'storage')).toMatchObject({
      implementation: '@kb-labs/adapters-fs', mode: 'real', baseDir: join(projectRoot, '.kb', 'data'), networkRequired: false,
    });
  });

  it('marks a default OpenAI binding as missing credentials without exposing values', async () => {
    const packageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-openai');
    await mkdir(join(packageRoot, 'dist'), { recursive: true });
    await writeFile(join(packageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-openai', main: 'dist/index.js' }));
    await writeFile(join(packageRoot, 'dist', 'index.js'), [
      'export const manifest = { id: "openai", defaultFor: ["llm"], configSchema: { apiKey: { type: "string", required: true, secret: true, env: "OPENAI_API_KEY" } } };',
      'export function createAdapter() { return {}; }',
    ].join('\n'));
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3',
      plugins: {},
      adapters: {
        openai: {
          version: '1.0.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-openai',
          enabled: true, source: 'platform',
        },
      },
    });
    vi.stubEnv('OPENAI_API_KEY', '');
    const { ctx, outputs } = context();
    mockAdapterHealth(ctx, [
      { slot: 'llm', mode: 'real', implementation: '@kb-labs/adapters-openai', configuredPackage: '@kb-labs/adapters-openai' },
    ]);

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    const adapters = dataOf<{ adapters: Array<Record<string, unknown>> }>(outputs[0]).adapters;
    expect(adapters.find(({ slot }) => slot === 'llm')).toMatchObject({
      implementation: '@kb-labs/adapters-openai', mode: 'real',
      credentialsStatus: 'missing', missingCredentials: ['OPENAI_API_KEY'],
    });
    expect(JSON.stringify(outputs[0])).not.toContain('configured-secret-value');
  });
});

describe('kb adapter bind', () => {
  // case: S-048.1
  it('declares bind, list, and unbind as platform-scoped commands', () => {
    expect(adapterBind.scope).toBe('platform');
    expect(adapterList.scope).toBe('platform');
    expect(adapterUnbind.scope).toBe('platform');
  });

  it('binds an installed adapter in platform config', async () => {
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3',
      plugins: {},
      adapters: {
        '@kb-labs/adapters-memory-cache': {
          version: '1.2.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-memory-cache',
          enabled: true, source: 'platform',
        },
      },
    });
    const adapterPackageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-memory-cache');
    await mkdir(adapterPackageRoot, { recursive: true });
    await writeFile(join(adapterPackageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-memory-cache', type: 'module', exports: './manifest.js' }));
    await writeFile(join(adapterPackageRoot, 'manifest.js'), 'export const manifest = { implements: "ICache", defaultFor: ["cache"] };');
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-memory-cache'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, data: { slot: 'cache', packageId: '@kb-labs/adapters-memory-cache', changed: true } });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toMatchObject({
      platform: { adapters: { cache: '@kb-labs/adapters-memory-cache' } },
    });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({});
  });

  it('accepts the camel-case vectorStore capability slot used by the runtime', async () => {
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3',
      plugins: {},
      adapters: {
        '@kb-labs/adapters-qdrant': {
          version: '1.2.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-qdrant',
          enabled: true, source: 'platform',
        },
      },
    });
    const adapterPackageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-qdrant');
    await mkdir(adapterPackageRoot, { recursive: true });
    await writeFile(join(adapterPackageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-qdrant', type: 'module', exports: './manifest.js' }));
    await writeFile(join(adapterPackageRoot, 'manifest.js'), 'export const manifest = { implements: "IVectorStore", defaultFor: ["vectorStore"] };');
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['vectorStore', '@kb-labs/adapters-qdrant'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, data: { slot: 'vectorStore', packageId: '@kb-labs/adapters-qdrant' } });
  });

  // case: S-211.7
  it('does not persist a lock-listed adapter that is absent from platform node_modules', async () => {
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3', plugins: {},
      adapters: {
        '@kb-labs/adapters-vibeproxy': {
          version: '1.0.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-vibeproxy',
          enabled: true, source: 'platform',
        },
      },
    });
    const before = await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8');
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['llm', '@kb-labs/adapters-vibeproxy'], { json: true });

    expect(code).toBe(1);
    expect(outputs[0]).toMatchObject({ ok: false, error: {
      code: 'KB_ADAPTER_NOT_INSTALLED',
      message: 'Adapter package @kb-labs/adapters-vibeproxy is not installed on this platform.',
      hint: 'kb marketplace install @kb-labs/adapters-vibeproxy --scope platform',
    } });
    expect(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8')).toBe(before);
  });

  // case: S-211.7
  it('does not bind an installed adapter whose manifest does not declare the requested slot', async () => {
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3', plugins: {},
      adapters: {
        '@kb-labs/adapters-storage-fixture': {
          version: '1.0.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-storage-fixture',
          enabled: true, source: 'platform',
        },
      },
    });
    const packageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-storage-fixture');
    await mkdir(packageRoot, { recursive: true });
    await writeFile(join(packageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-storage-fixture', type: 'module', exports: './manifest.js' }));
    await writeFile(join(packageRoot, 'manifest.js'), 'export const manifest = { implements: "IStorage", defaultFor: ["storage"] };');
    const before = await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8');
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['llm', '@kb-labs/adapters-storage-fixture'], { json: true });

    expect(code).toBe(1);
    expect(outputs[0]).toMatchObject({ ok: false, error: {
      code: 'KB_ADAPTER_SLOT_MISMATCH',
      message: 'Adapter package @kb-labs/adapters-storage-fixture does not declare platform slot llm.',
      details: { package: '@kb-labs/adapters-storage-fixture', slot: 'llm' },
      hint: 'Install a provider that declares llm with `kb marketplace install <pkg> --scope platform`, then retry.',
    } });
    expect(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8')).toBe(before);
  });

  // case: S-051.4, S-211.7
  it('returns the catalogued install error for an absent package through both binding commands', async () => {
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['storage', '@kb-labs/not-installed'], { json: true });
    const bindError = (outputs[0] as { error: Record<string, unknown> }).error;

    expect(code).toBe(1);
    expect(bindError).toMatchObject({
      code: 'KB_ADAPTER_NOT_INSTALLED',
      message: 'Adapter package @kb-labs/not-installed is not installed on this platform.',
      hint: 'kb marketplace install @kb-labs/not-installed --scope platform',
    });

    const configContext = context();
    const configCode = await configSet.run(configContext.ctx, [
      'platform.adapters.storage', '@kb-labs/not-installed',
    ], { scope: 'platform', json: true });
    const configError = (configContext.outputs[0] as { error: Record<string, unknown> }).error;

    expect(configCode).toBe(1);
    expect(configError).toEqual(bindError);
  });

  // case: S-051.3, S-203.4
  it('renders the missing adapter next step through the shared human presenter', async () => {
    const { ctx, errors } = context();

    const code = await adapterBind.run(ctx, ['storage', '@kb-labs/not-installed'], {});

    expect(code).toBe(1);
    expect(errors).toEqual([
      'Adapter package @kb-labs/not-installed is not installed on this platform. [KB_ADAPTER_NOT_INSTALLED]\n' +
      '  Hint:  kb marketplace install @kb-labs/not-installed --scope platform\n' +
      '  Correlation ID: adapter-bind-test',
    ]);
    expect(errors[0]).not.toContain('Cause:');
  });

  it('validates a binding without writing in dry-run mode', async () => {
    await writeMarketplaceLock(platformRoot, {
      schema: 'kb.marketplace/3',
      plugins: {},
      adapters: {
        '@kb-labs/adapters-memory-cache': {
          version: '1.2.0', integrity: 'sha256-fixture', resolvedPath: 'node_modules/@kb-labs/adapters-memory-cache',
          enabled: true, source: 'platform',
        },
      },
    });
    const adapterPackageRoot = join(platformRoot, 'node_modules', '@kb-labs', 'adapters-memory-cache');
    await mkdir(adapterPackageRoot, { recursive: true });
    await writeFile(join(adapterPackageRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-memory-cache', type: 'module', exports: './manifest.js' }));
    await writeFile(join(adapterPackageRoot, 'manifest.js'), 'export const manifest = { implements: "ICache", defaultFor: ["cache"] };');
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-memory-cache'], { json: true, 'dry-run': true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, data: { dryRun: true, changed: true } });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({});
  });
});

describe('kb adapter unbind', () => {
  it('clears the platform binding and its options', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' }, adapterOptions: { storage: { baseDir: '.kb/data' } } },
    }));
    const { ctx, outputs } = context();

    const code = await adapterUnbind.run(ctx, ['storage'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, data: { slot: 'storage', changed: true, dryRun: false } });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({ platform: { adapters: {}, adapterOptions: {} } });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({});
  });

  it('validates an unbind without writing in dry-run mode', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({ platform: { adapters: { storage: '@kb-labs/adapters-fs' } } }));
    const { ctx, outputs } = context();

    const code = await adapterUnbind.run(ctx, ['storage'], { json: true, 'dry-run': true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, data: { dryRun: true, changed: true } });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' } },
    });
  });
});
