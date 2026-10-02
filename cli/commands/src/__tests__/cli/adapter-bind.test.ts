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

let sandbox: string;
let platformRoot: string;
let projectRoot: string;

function context() {
  const outputs: unknown[] = [];
  const ctx: PluginContextV3 = {
    host: 'cli',
    requestId: 'adapter-bind-test',
    pluginId: '@kb-labs/system',
    pluginVersion: '1.0.0',
    cwd: projectRoot,
    ui: { ...noopUI, json: (data: unknown) => outputs.push(data) },
    platform: { logger: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn(), child: vi.fn().mockReturnThis() } } as never,
    runtime: { fs: {} as never, fetch: vi.fn(), env: vi.fn() },
    api: {} as never,
    hostContext: { host: 'cli', argv: [], flags: {} },
    trace: noopTraceContext,
  };
  return { ctx, outputs };
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

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true });
    const adapters = (outputs[0] as { adapters: Array<Record<string, unknown>> }).adapters;
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

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    const adapters = (outputs[0] as { adapters: Array<Record<string, unknown>> }).adapters;
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

    const code = await adapterList.run(ctx, [], { json: true });

    expect(code).toBe(0);
    const adapters = (outputs[0] as { adapters: Array<Record<string, unknown>> }).adapters;
    expect(adapters.find(({ slot }) => slot === 'llm')).toMatchObject({
      implementation: '@kb-labs/adapters-openai', mode: 'real',
      credentialsStatus: 'missing', missingCredentials: ['OPENAI_API_KEY'],
    });
    expect(JSON.stringify(outputs[0])).not.toContain('configured-secret-value');
  });
});

describe('kb adapter bind', () => {
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
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-memory-cache'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, slot: 'cache', packageId: '@kb-labs/adapters-memory-cache', changed: true });
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
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['vectorStore', '@kb-labs/adapters-qdrant'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, slot: 'vectorStore', packageId: '@kb-labs/adapters-qdrant' });
  });

  // case: S-051.3, S-051.4
  it('returns the shared adapter install error envelope when the adapter is not installed', async () => {
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-missing'], { json: true });

    expect(code).toBe(1);
    expect(outputs[0]).toMatchObject({
      ok: false, error: {
        code: 'KB_ADAPTER_NOT_INSTALLED',
        message: 'Adapter package @kb-labs/adapters-missing is not installed on this platform.',
        hint: 'kb marketplace install @kb-labs/adapters-missing --scope platform',
      },
    });
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
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-memory-cache'], { json: true, 'dry-run': true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, dryRun: true, changed: true });
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
    expect(outputs[0]).toMatchObject({ ok: true, slot: 'storage', changed: true, dryRun: false });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({ platform: { adapters: {}, adapterOptions: {} } });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({});
  });

  it('validates an unbind without writing in dry-run mode', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({ platform: { adapters: { storage: '@kb-labs/adapters-fs' } } }));
    const { ctx, outputs } = context();

    const code = await adapterUnbind.run(ctx, ['storage'], { json: true, 'dry-run': true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, dryRun: true, changed: true });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' } },
    });
  });
});
