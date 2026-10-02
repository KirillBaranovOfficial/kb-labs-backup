/**
 * Tests for `kb config get | set | show` against real files in temp
 * directories: platform and project roots are selected through the same env
 * variables the CLI uses (KB_PLATFORM_ROOT / KB_PROJECT_ROOT), nothing under the
 * real HOME is touched.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { promises as fsp } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { noopUI, noopTraceContext } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';

import { configGet } from '../../commands/system/config/get.js';
import { configSet } from '../../commands/system/config/set.js';
import { configShow } from '../../commands/system/config/show.js';
import { createProjectRegistry } from '@kb-labs/core-project-registry';

// ── Harness ──────────────────────────────────────────────────────────────────

interface Captured {
  json: Array<Record<string, unknown>>;
  errors: string[];
  written: string[];
  success: Array<{ title: string; items: string[] }>;
}

function makeCtx(cwd: string): { ctx: PluginContextV3; out: Captured } {
  const out: Captured = { json: [], errors: [], written: [], success: [] };
  const ctx: PluginContextV3 = {
    host: 'cli',
    requestId: 'test-config-get-set',
    pluginId: '@kb-labs/system',
    pluginVersion: '1.0.0',
    cwd,
    ui: {
      ...noopUI,
      json: (data: unknown) => {
        out.json.push(data as Record<string, unknown>);
      },
      error: (message: unknown) => {
        out.errors.push(String(message));
      },
      write: (text: string) => {
        out.written.push(text);
      },
      success: (title, options) => {
        out.success.push({ title, items: (options?.sections?.flatMap((s) => s.items) ?? []).filter((item): item is string => typeof item === 'string') });
      },
    },
    platform: {
      logger: { info: vi.fn(), warn: vi.fn(), error: vi.fn(), debug: vi.fn(), child: vi.fn().mockReturnThis() },
    } as never,
    runtime: { fs: {} as never, fetch: vi.fn(), env: vi.fn() },
    api: {} as never,
    hostContext: { host: 'cli' as const, argv: [], flags: {} },
    trace: noopTraceContext,
  };
  return { ctx, out };
}

let tmp: string;
let platformRoot: string;
let projectRoot: string;
let projectId: string;

async function write(root: string, rel: string, contents: string): Promise<string> {
  const full = path.join(root, rel);
  await fsp.mkdir(path.dirname(full), { recursive: true });
  await fsp.writeFile(full, contents, 'utf8');
  return full;
}

async function writeCommitSettingsManifest(): Promise<void> {
  const pluginRoot = path.join(platformRoot, '.kb', 'plugins', 'commit');
  const packageJson = JSON.stringify({ name: '@kb-labs/commit', version: '0.1.0' });
  await write(pluginRoot, 'package.json', packageJson);
  await write(pluginRoot, 'kb.plugin.json', JSON.stringify({
    schema: 'kb.plugin/3',
    id: '@kb-labs/commit',
    version: '0.1.0',
    configSection: 'commit',
    settings: {
      defaults: { enabled: true, label: 'Commit', scope: { default: 'root', scopes: [] }, llm: { enabled: true, temperature: 0.3, maxTokens: 2000 }, storage: { directory: '.kb/commit' }, git: { protectedBranches: ['main'], autoStage: false } },
      schema: {
        type: 'object',
        properties: {
          enabled: { type: 'boolean' },
          label: { type: 'string' },
          scope: { type: 'object', properties: { default: { type: 'string' }, scopes: { type: 'array', items: { type: 'object' } } } },
          llm: { type: 'object', properties: { enabled: { type: 'boolean' }, temperature: { type: 'number' }, maxTokens: { type: 'integer', minimum: 1 } } },
          storage: { type: 'object', properties: { directory: { type: 'string' } } },
          git: { type: 'object', properties: { protectedBranches: { type: 'array' }, autoStage: { type: 'boolean' } } },
        },
        additionalProperties: true,
      },
    },
  }));
  const integrity = `sha256-${createHash('sha256').update(packageJson).digest('base64')}`;
  await write(platformRoot, '.kb/marketplace.lock', JSON.stringify({
    schema: 'kb.marketplace/3',
    plugins: {
      '@kb-labs/commit': { source: 'platform', version: '0.1.0', integrity, resolvedPath: '.kb/plugins/commit', enabled: true },
    },
  }));
}

const PLATFORM_CONFIG = `{
  // platform baseline, hand-written notes live here
  "platform": {
    "adapters": { "llm": "@kb-labs/adapters-openai", "cache": null },
    "adapterOptions": { "llm": { "defaultModel": "gpt-4o-mini" } }
  }
}
`;

const PROJECT_CONFIG = `{
  // project settings
}
`;

async function run(
  command: { run: (ctx: PluginContextV3, argv: string[], flags: Record<string, unknown>) => Promise<number> },
  argv: string[],
  flags: Record<string, unknown> = {},
): Promise<{ code: number; out: Captured }> {
  const { ctx, out } = makeCtx(projectRoot);
  const code = await command.run(ctx, argv, { json: true, ...flags });
  return { code, out };
}

beforeEach(async () => {
  tmp = await fsp.mkdtemp(path.join(os.tmpdir(), 'kb-config-cmd-'));
  platformRoot = path.join(tmp, 'platform');
  projectRoot = path.join(tmp, 'project');
  await fsp.mkdir(path.join(platformRoot, 'node_modules', '@kb-labs', 'cli-bin'), { recursive: true });
  await fsp.mkdir(projectRoot, { recursive: true });
  await write(platformRoot, '.kb/kb.config.jsonc', PLATFORM_CONFIG);
  await write(projectRoot, '.kb/kb.config.jsonc', PROJECT_CONFIG);
  await writeCommitSettingsManifest();
  vi.stubEnv('KB_PLATFORM_ROOT', platformRoot);
  vi.stubEnv('KB_PROJECT_ROOT', projectRoot);
  vi.stubEnv('KB_HOME', path.join(tmp, 'platform-home'));
  projectId = (await createProjectRegistry().add(projectRoot)).project.id;
});

afterEach(async () => {
  vi.unstubAllEnvs();
  delete process.env.KB_SOCKET_HASH;
  await fsp.rm(tmp, { recursive: true, force: true });
});

const PROJECT_FILE = () => path.join(projectRoot, '.kb', 'kb.config.jsonc');
const PLATFORM_FILE = () => path.join(platformRoot, '.kb', 'kb.config.jsonc');
const PROJECT_SETTINGS_FILE = () => path.join(tmp, 'platform-home', 'state', projectId, 'config.jsonc');

// ── config set ───────────────────────────────────────────────────────────────

describe('config set', () => {
  it('writes project-owned settings to the platform state directory', async () => {
    const { code, out } = await run(configSet, ['plugins.commit.scope.default', 'My commit']);

    expect(code).toBe(0);
    expect(out.json[0]).toMatchObject({
      ok: true,
      key: 'plugins.commit.scope.default',
      scope: 'project',
      file: PROJECT_SETTINGS_FILE(),
      changed: true,
      dryRun: false,
      value: 'My commit',
    });
    const written = await fsp.readFile(String(out.json[0]?.file), 'utf8');
    expect(JSON.parse(written.replace(/\/\/.*$/gm, ''))).toMatchObject({
      plugins: { commit: { scope: { default: 'My commit' } } },
    });
    expect(await fsp.readFile(PROJECT_FILE(), 'utf8')).toBe(PROJECT_CONFIG);
    expect(await fsp.readFile(PLATFORM_FILE(), 'utf8')).toBe(PLATFORM_CONFIG);
  });

  it('--scope platform writes the platform user config', async () => {
    const { code } = await run(configSet, ['platform.adapters.llm', '@kb-labs/adapters-vibeproxy'], {
      scope: 'platform',
    });
    expect(code).toBe(0);
    expect(await fsp.readFile(PLATFORM_FILE(), 'utf8')).toBe(
      PLATFORM_CONFIG.replace('@kb-labs/adapters-openai', '@kb-labs/adapters-vibeproxy'),
    );
    expect(await fsp.readFile(PROJECT_FILE(), 'utf8')).toBe(PROJECT_CONFIG);
  });

  // case: S-204.5
  it('rejects invalid platform plugin settings before changing the platform file', async () => {
    const before = await fsp.readFile(PLATFORM_FILE(), 'utf8');
    const { code, out } = await run(configSet, ['plugins.commit.llm.maxTokens', '0'], { scope: 'platform' });

    expect(code).toBe(1);
    expect((out.json[0] as { error: { code: string; hint: string } }).error).toMatchObject({
      code: 'KB_PLUGIN_SETTINGS_INVALID',
      hint: expect.stringContaining('declared settings schema'),
    });
    expect(await fsp.readFile(PLATFORM_FILE(), 'utf8')).toBe(before);
  });

  // case: S-204.9
  it('merges and attributes manifest, platform, then project settings', async () => {
    const manifestDefault = await run(configGet, ['plugins.commit.llm.maxTokens']);
    expect(manifestDefault.out.json[0]).toMatchObject({ value: 2000, layer: 'manifest' });

    expect((await run(configSet, ['plugins.commit.llm.maxTokens', '1900'], { scope: 'platform' })).code).toBe(0);
    expect((await run(configGet, ['plugins.commit.llm.maxTokens'])).out.json[0])
      .toMatchObject({ value: 1900, layer: 'platform' });

    expect((await run(configSet, ['plugins.commit.llm.maxTokens', '1800'])).code).toBe(0);
    expect((await run(configGet, ['plugins.commit.llm.maxTokens'])).out.json[0])
      .toMatchObject({ value: 1800, layer: 'project' });
    expect((await run(configGet, ['plugins.commit.llm.temperature'])).out.json[0])
      .toMatchObject({ value: 0.3, layer: 'manifest' });
  });

  it('--dry-run reports the outcome and writes nothing', async () => {
    const { code, out } = await run(configSet, ['plugins.commit.scope.default', 'Dry run'], { 'dry-run': true });
    expect(code).toBe(0);
    expect(out.json[0]).toMatchObject({ ok: true, dryRun: true, changed: true });
    await expect(fsp.access(String(out.json[0]?.file))).rejects.toThrow();
  });

  it('parses JSON values and lets --string keep the text', async () => {
    await run(configSet, ['plugins.commit.llm.maxTokens', '4']);
    await run(configSet, ['plugins.commit.scope.default', '42'], { string: true });
    const written = JSON.parse(await fsp.readFile(PROJECT_SETTINGS_FILE(), 'utf8')) as { plugins: { commit: Record<string, unknown> } };
    expect((written.plugins.commit.llm as Record<string, unknown>).maxTokens).toBe(4);
    expect((written.plugins.commit.scope as Record<string, unknown>).default).toBe('42');
  });

  it('rejects project-scoped adapter settings with a move-to-platform hint', async () => {
    for (const key of ['platform.adapterOptions.llm.defaultModel', 'adapters.llm', 'adapterOptions.llm.apiKey']) {
      const { code, out } = await run(configSet, [key, 'configured-value']);
      expect(code).toBe(1);
      expect((out.json[0] as { error: { code: string; hint: string } }).error).toMatchObject({
        code: 'KB_ADAPTER_PROJECT_SCOPE',
        hint: expect.stringContaining('Move `platform.adapters` and `platform.adapterOptions` to the platform config'),
      });
    }
  });

  it('serialises concurrent sets from several commands', async () => {
    await Promise.all([
      run(configSet, ['plugins.commit.enabled', 'false']),
      run(configSet, ['plugins.commit.llm.enabled', 'false']),
      run(configSet, ['plugins.commit.llm.temperature', '0.5']),
      run(configSet, ['plugins.commit.llm.maxTokens', '1400']),
      run(configSet, ['plugins.commit.storage.directory', 'tmp/commit']),
    ]);
    const parsed = JSON.parse(await fsp.readFile(PROJECT_SETTINGS_FILE(), 'utf8')) as {
      plugins: { commit: { enabled: boolean; llm: { enabled: boolean; temperature: number; maxTokens: number }; storage: { directory: string } } };
    };
    expect(parsed.plugins.commit).toMatchObject({
      enabled: false,
      llm: { enabled: false, temperature: 0.5, maxTokens: 1400 },
      storage: { directory: 'tmp/commit' },
    });
  });

  describe('errors use the unified envelope', () => {
    it('unknown key: KB_CONFIG_UNKNOWN_KEY with a "did you mean" hint', async () => {
      const { code, out } = await run(configSet, ['platform.adaptors.llm', 'x'], { scope: 'platform' });
      expect(code).toBe(1);
      const envelope = (out.json[0] as { ok: boolean; error: Record<string, unknown> }).error;
      expect(out.json[0]?.ok).toBe(false);
      expect(envelope).toMatchObject({ code: 'KB_CONFIG_UNKNOWN_KEY', area: 'config', severity: 'error' });
      expect(envelope.hint).toContain('Did you mean "platform.adapters.llm"?');
      expect(String(envelope.hint).length).toBeGreaterThan(0);
      expect(await fsp.readFile(PROJECT_FILE(), 'utf8')).toBe(PROJECT_CONFIG);
    });

    it('invalid value: KB_CONFIG_INVALID names the field path', async () => {
      const { code, out } = await run(configSet, ['platform.adapters.llm', '42'], { scope: 'platform' });
      expect(code).toBe(1);
      const envelope = (out.json[0] as { error: { code: string; message: string; hint: string } }).error;
      expect(envelope.code).toBe('KB_CONFIG_INVALID');
      expect(envelope.message).toContain('platform.adapters.llm');
      expect(envelope.hint).toContain('platform.adapters.llm');
    });

    it('invalid enum value under execution', async () => {
      const { out } = await run(configSet, ['platform.execution.mode', 'turbo']);
      expect((out.json[0] as { error: { code: string; message: string } }).error).toMatchObject({
        code: 'KB_CONFIG_INVALID',
        message: expect.stringContaining('platform.execution.mode'),
      });
    });

    it('rejects JSON-looking values that do not parse', async () => {
      const { out } = await run(configSet, ['plugins.commit.list', '[1,']);
      expect((out.json[0] as { error: { code: string } }).error.code).toBe('KB_CONFIG_INVALID');
    });

    it('human output carries message, cause, hint and the code', async () => {
      const { ctx, out } = makeCtx(projectRoot);
      const code = await configSet.run(ctx, ['platform.adaptors.llm', 'x'], { scope: 'platform' });
      expect(code).toBe(1);
      expect(out.json).toEqual([]);
      const text = out.errors.join('\n');
      expect(text).toContain('[KB_CONFIG_UNKNOWN_KEY]');
      expect(text).toContain('Hint:');
    });
  });

  describe('secrets', () => {
    it('refuses a raw secret and never echoes it', async () => {
      const { code, out } = await run(configSet, ['platform.adapterOptions.llm.apiKey', 'not-a-real-key-123'], { scope: 'platform' });
      expect(code).toBe(1);
      const envelope = (out.json[0] as { error: { code: string; hint: string } }).error;
      expect(envelope.code).toBe('KB_CONFIG_SECRET_PLAINTEXT');
      expect(envelope.hint).toContain("kb config set --scope platform platform.adapterOptions.llm.apiKey '${LLM_API_KEY}'");
      expect(JSON.stringify(out.json)).not.toContain('not-a-real-key-123');
      expect(await fsp.readFile(PROJECT_FILE(), 'utf8')).toBe(PROJECT_CONFIG);
    });

    it('stores a ${ENV_VAR} reference when the variable is set, and shows it unredacted', async () => {
      vi.stubEnv('KB_TEST_LLM_KEY', 'present');
      const { code, out } = await run(configSet, ['platform.adapterOptions.llm.apiKey', '${KB_TEST_LLM_KEY}'], { scope: 'platform' });
      expect(code).toBe(0);
      expect(out.json[0]?.value).toBe('${KB_TEST_LLM_KEY}');
      expect(await fsp.readFile(PLATFORM_FILE(), 'utf8')).toContain('"apiKey": "${KB_TEST_LLM_KEY}"');
    });

    it('KB_CONFIG_SECRET_MISSING for a reference to an unset variable; --allow-missing-env overrides', async () => {
      vi.stubEnv('KB_TEST_UNSET_KEY', undefined);
      const refused = await run(configSet, ['platform.adapterOptions.llm.apiKey', '${KB_TEST_UNSET_KEY}'], { scope: 'platform' });
      expect(refused.code).toBe(1);
      expect((refused.out.json[0] as { error: { code: string; hint: string } }).error).toMatchObject({
        code: 'KB_CONFIG_SECRET_MISSING',
        hint: expect.stringContaining('environment variable'),
      });

      const allowed = await run(configSet, ['platform.adapterOptions.llm.apiKey', '${KB_TEST_UNSET_KEY}'], {
        scope: 'platform',
        'allow-missing-env': true,
      });
      expect(allowed.code).toBe(0);
    });

    it('--allow-plain-secret writes the raw value but output stays redacted', async () => {
      const { code, out } = await run(configSet, ['platform.adapterOptions.llm.apiKey', 'throwaway-local'], {
        scope: 'platform',
        'allow-plain-secret': true,
      });
      expect(code).toBe(0);
      expect(out.json[0]?.value).toBe('***REDACTED***');
      expect(await fsp.readFile(PLATFORM_FILE(), 'utf8')).toContain('"apiKey": "throwaway-local"');
    });
  });

  describe('adapter manifest configSchema', () => {
    beforeEach(async () => {
      const pkg = path.join(platformRoot, 'node_modules', '@fake', 'adapter');
      await write(
        pkg,
        'package.json',
        JSON.stringify({ name: '@fake/adapter', type: 'module', exports: { '.': { import: './index.js' } } }),
      );
      await write(
        pkg,
        'index.js',
        `export const manifest = { configSchema: {
          temperature: { type: 'number' },
          mode: { type: 'string', enum: ['fast', 'careful'] },
        } };\n`,
      );
      await write(
        platformRoot,
        '.kb/kb.config.jsonc',
        `{ "platform": { "adapters": { "llm": "@fake/adapter" } } }\n`,
      );
    });

    it('rejects a wrongly typed option declared by the adapter manifest', async () => {
      const { code, out } = await run(configSet, ['platform.adapterOptions.llm.temperature', '"hot"'], { scope: 'platform' });
      expect(code).toBe(1);
      expect((out.json[0] as { error: { code: string; message: string } }).error).toMatchObject({
        code: 'KB_CONFIG_INVALID',
        message: expect.stringContaining('platform.adapterOptions.llm.temperature'),
      });
    });

    it('rejects a value outside the manifest enum', async () => {
      const { out } = await run(configSet, ['platform.adapterOptions.llm.mode', 'reckless'], { scope: 'platform' });
      expect((out.json[0] as { error: { cause: string } }).error.cause).toContain('Expected one of: fast, careful');
    });

    it('accepts valid and undeclared options', async () => {
      expect((await run(configSet, ['platform.adapterOptions.llm.temperature', '0.4'], { scope: 'platform' })).code).toBe(0);
      expect((await run(configSet, ['platform.adapterOptions.llm.somethingElse', 'x'], { scope: 'platform' })).code).toBe(0);
      const ok = await run(configSet, ['platform.adapterOptions.llm.mode', 'fast'], { scope: 'platform' });
      expect(ok.out.json[0]?.adapterSchemaChecked).toBe(true);
    });

    it('skips the manifest check when the adapter package is not installed', async () => {
      await write(platformRoot, '.kb/kb.config.jsonc', `{ "platform": { "adapters": { "llm": "@not/installed" } } }\n`);
      expect((await run(configSet, ['platform.adapterOptions.llm.temperature', '"hot"'], { scope: 'platform' })).code).toBe(0);
    });
  });
});

// ── config get ───────────────────────────────────────────────────────────────

describe('config get', () => {
  // case: S-204.7
  it('returns KB_PROJECT_UNKNOWN outside the registered project context', async () => {
    await createProjectRegistry().remove(projectId);
    const { code, out } = await run(configGet, ['plugins.commit.scope.scopes']);
    expect(code).toBe(1);
    expect((out.json[0] as { error: { code: string; hint: string } }).error).toMatchObject({
      code: 'KB_PROJECT_UNKNOWN',
      hint: expect.stringContaining('kb project add'),
    });
  });

  it('returns the effective value with layer and file provenance', async () => {
    const { code, out } = await run(configGet, ['platform.adapterOptions.llm.defaultModel']);
    expect(code).toBe(0);
    expect(out.json[0]).toMatchObject({
      ok: true,
      key: 'platform.adapterOptions.llm.defaultModel',
      scope: 'effective',
      found: true,
      value: 'gpt-4o-mini',
      layer: 'platform',
      source: PLATFORM_FILE(),
    });
  });

  it('attributes platform values to the platform layer and generated values to the generated layer', async () => {
    await write(platformRoot, '.kb/generated/topology.json', '{ "gateway": { "port": 4000 } }');
    const platformValue = await run(configGet, ['platform.adapters.llm']);
    expect(platformValue.out.json[0]).toMatchObject({ value: '@kb-labs/adapters-openai', layer: 'platform', source: PLATFORM_FILE() });
    const generatedValue = await run(configGet, ['gateway.port']);
    expect(generatedValue.out.json[0]).toMatchObject({
      value: 4000,
      layer: 'generated',
      source: path.join(platformRoot, '.kb/generated/topology.json'),
    });
  });

  it('rejects project adapter overrides and continues to read the platform binding', async () => {
    const rejected = await run(configSet, ['platform.adapters.llm', '@kb-labs/adapters-vibeproxy']);
    expect(rejected.code).toBe(1);
    expect((rejected.out.json[0] as { error: { code: string } }).error.code).toBe('KB_ADAPTER_PROJECT_SCOPE');
    const { out } = await run(configGet, ['platform.adapters.llm']);
    expect(out.json[0]).toMatchObject({ value: '@kb-labs/adapters-openai', layer: 'platform', source: PLATFORM_FILE() });
  });

  it('a container reports the highest layer beneath it', async () => {
    const { out } = await run(configGet, ['platform.adapters']);
    expect(out.json[0]).toMatchObject({ found: true, layer: 'platform', value: { llm: '@kb-labs/adapters-openai', cache: null } });
  });

  it('a valid key that is not set is not an error', async () => {
    const { code, out } = await run(configGet, ['platform.execution.mode']);
    expect(code).toBe(0);
    expect(out.json[0]).toMatchObject({ ok: true, found: false, value: null });
  });

  it('an unknown key is KB_CONFIG_UNKNOWN_KEY with a suggestion', async () => {
    const { code, out } = await run(configGet, ['platform.adapterz.llm']);
    expect(code).toBe(1);
    expect((out.json[0] as { error: { code: string; hint: string } }).error).toMatchObject({
      code: 'KB_CONFIG_UNKNOWN_KEY',
      hint: expect.stringContaining('platform.adapters.llm'),
    });
  });

  it('redacts raw secrets but shows references', async () => {
    await write(
      platformRoot,
      '.kb/kb.config.jsonc',
      `{ "platform": { "adapterOptions": { "llm": { "apiKey": "raw-secret-value", "orgKey": "\${ORG_KEY}" } } } }\n`,
    );
    const raw = await run(configGet, ['platform.adapterOptions.llm.apiKey']);
    expect(raw.out.json[0]?.value).toBe('***REDACTED***');
    expect(JSON.stringify(raw.out.json)).not.toContain('raw-secret-value');
    const ref = await run(configGet, ['platform.adapterOptions.llm.orgKey']);
    expect(ref.out.json[0]?.value).toBe('${ORG_KEY}');
    const container = await run(configGet, ['platform.adapterOptions.llm']);
    expect(JSON.stringify(container.out.json)).not.toContain('raw-secret-value');
  });

  it('prints just the value in human mode', async () => {
    const { ctx, out } = makeCtx(projectRoot);
    const code = await configGet.run(ctx, ['platform.adapters.llm'], {});
    expect(code).toBe(0);
    expect(out.written.join('')).toBe('@kb-labs/adapters-openai\n');
  });
});

// ── config show ──────────────────────────────────────────────────────────────

describe('config show provenance', () => {
  it('adds layer and file per field, including the generated layer', async () => {
    await write(platformRoot, '.kb/generated/topology.json', '{ "gateway": { "port": 4000 } }');
    const { code, out } = await run(configShow, []);
    expect(code).toBe(0);
    const payload = out.json[0] as { generated?: string[]; fields: Array<{ field: string; layer?: string; file?: string }> };
    const byField = new Map(payload.fields.map((row) => [row.field, row]));

    expect(byField.get('adapters.llm')).toMatchObject({ layer: 'platform', file: PLATFORM_FILE() });
    expect(byField.get('adapterOptions.llm.defaultModel')).toMatchObject({ layer: 'platform', file: PLATFORM_FILE() });
    expect(byField.get('gateway.port')).toMatchObject({ layer: 'generated' });
    expect(payload.generated).toEqual([path.join(platformRoot, '.kb/generated/topology.json')]);
  });

  it('human output has a LAYER column', async () => {
    const { ctx, out } = makeCtx(projectRoot);
    await configShow.run(ctx, [], {});
    const lines = out.success[0]?.items ?? [];
    expect(lines[0]).toMatch(/SOURCE\s+LAYER\s+FIELD\s+VALUE/);
    expect(lines.some((line) => /platform\s+platform\s+adapters\.llm/.test(line))).toBe(true);
  });
});
