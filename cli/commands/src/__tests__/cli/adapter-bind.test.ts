import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { noopTraceContext, noopUI } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
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
  platformRoot = join(sandbox, 'platform');
  projectRoot = join(sandbox, 'project');
  await mkdir(join(platformRoot, '.kb'), { recursive: true });
  await mkdir(join(projectRoot, '.kb'), { recursive: true });
  await writeFile(join(platformRoot, '.kb', 'kb.config.json'), '{}');
  await writeFile(join(projectRoot, '.kb', 'kb.config.json'), '{}');
  vi.stubEnv('KB_PLATFORM_ROOT', platformRoot);
  vi.stubEnv('KB_PROJECT_ROOT', projectRoot);
});

afterEach(async () => {
  vi.unstubAllEnvs();
  await rm(sandbox, { recursive: true, force: true });
});

describe('kb adapter list', () => {
  it('shows installed platform defaults and project effective storage options', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({
      platform: { adapters: {
        storage: '@kb-labs/adapters-fs@2.120.1',
        cache: '@kb-labs/adapters-state-broker',
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
  });
});

describe('kb adapter bind', () => {
  it('binds an installed machine adapter in the current project config', async () => {
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
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toMatchObject({
      platform: { adapters: { cache: '@kb-labs/adapters-memory-cache' } },
    });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({});
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

  it('gives the exact install command when the adapter is not installed', async () => {
    const { ctx, outputs } = context();

    const code = await adapterBind.run(ctx, ['cache', '@kb-labs/adapters-missing'], { json: true });

    expect(code).toBe(1);
    expect(outputs[0]).toMatchObject({
      ok: false,
      error: 'Adapter package @kb-labs/adapters-missing is not installed on this platform.',
      hint: 'Run: kb marketplace install @kb-labs/adapters-missing --scope platform',
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
  it('clears the project binding and its options while preserving platform defaults', async () => {
    await writeFile(join(platformRoot, '.kb', 'kb.config.json'), JSON.stringify({ platform: { adapters: { storage: '@kb-labs/adapters-fs' } } }));
    await writeFile(join(projectRoot, '.kb', 'kb.config.json'), JSON.stringify({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' }, adapterOptions: { storage: { baseDir: '.kb/data' } } },
    }));
    const { ctx, outputs } = context();

    const code = await adapterUnbind.run(ctx, ['storage'], { json: true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, slot: 'storage', changed: true, dryRun: false });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({ platform: { adapters: {}, adapterOptions: {} } });
    expect(JSON.parse(await readFile(join(platformRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' } },
    });
  });

  it('validates an unbind without writing in dry-run mode', async () => {
    await writeFile(join(projectRoot, '.kb', 'kb.config.json'), JSON.stringify({ platform: { adapters: { storage: '@kb-labs/adapters-fs' } } }));
    const { ctx, outputs } = context();

    const code = await adapterUnbind.run(ctx, ['storage'], { json: true, 'dry-run': true });

    expect(code).toBe(0);
    expect(outputs[0]).toMatchObject({ ok: true, dryRun: true, changed: true });
    expect(JSON.parse(await readFile(join(projectRoot, '.kb', 'kb.config.json'), 'utf8'))).toEqual({
      platform: { adapters: { storage: '@kb-labs/adapters-fs' } },
    });
  });
});
