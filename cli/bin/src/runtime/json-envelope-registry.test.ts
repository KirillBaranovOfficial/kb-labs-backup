import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { registerBuiltinCommands, registerBuiltinSystemCommands, registry, type RouteResult } from '@kb-labs/cli-commands';
import { createProjectRegistry } from '@kb-labs/core-project-registry';

const mocks = vi.hoisted(() => {
  const logger = {
    trace(message: string) { process.stderr.write(`[trace] ${message}\n`); },
    debug(message: string) { process.stderr.write(`[debug] ${message}\n`); },
    info(message: string) { process.stderr.write(`[info] ${message}\n`); },
    warn(message: string) { process.stderr.write(`[warn] ${message}\n`); },
    error(message: string) { process.stderr.write(`[error] ${message}\n`); },
    fatal(message: string) { process.stderr.write(`[fatal] ${message}\n`); },
    child() { return logger; },
  };
  const corePlatform = {
    logger,
    logs: {
      query: vi.fn().mockResolvedValue({ logs: [] }),
      getById: vi.fn().mockResolvedValue({
        id: 'fixture-log-id',
        timestamp: Date.now(),
        level: 'info',
        source: 'registry-test',
        message: 'fixture log record',
        fields: { level: 30 },
      }),
      search: vi.fn().mockResolvedValue({ logs: [] }),
      getStats: vi.fn().mockResolvedValue({}),
      getCapabilities: vi.fn().mockResolvedValue({}),
    },
  };
  return {
    loadPlatformConfiguration: vi.fn(),
    initializePlatform: vi.fn(),
    resolveProjectConfig: vi.fn(),
    logger,
    platform: { logger },
    corePlatform,
    platformSync: vi.fn().mockResolvedValue({
      ok: true,
      status: 'success',
      mode: 'validate',
      checked: 0,
      missing: [],
      mismatched: [],
      installed: [],
      errors: [],
      lockMissing: true,
    }),
    loadCredentials: vi.fn().mockResolvedValue({
      gatewayUrl: 'https://gateway.test',
      accessToken: 'registry-test-token',
      expiresAt: Date.now() + 60 * 60 * 1000,
    }),
  };
});

vi.mock('./platform-init.js', () => ({
  loadPlatformConfiguration: mocks.loadPlatformConfiguration,
  initializePlatform: mocks.initializePlatform,
  resolveProjectConfig: mocks.resolveProjectConfig,
}));
vi.mock('@kb-labs/core-runtime', async (importOriginal) => ({
  ...await importOriginal<typeof import('@kb-labs/core-runtime')>(),
  platform: mocks.corePlatform,
  platformSync: mocks.platformSync,
}));
vi.mock('@kb-labs/cli-runtime/gateway', async (importOriginal) => ({
  ...await importOriginal<typeof import('@kb-labs/cli-runtime/gateway')>(),
  resolveActiveGateway: async () => ({ gatewayUrl: 'https://gateway.test' }),
  gatewayApiFetch: async (input: string | URL) => {
    const data = String(input).includes('/api/v1/webhooks') ? { webhooks: [] } : { tokens: [] };
    return new Response(JSON.stringify({ ok: true, data }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  },
  CredentialsManager: class {
    load() { return mocks.loadCredentials(); }
    isExpired() { return false; }
  },
}));

import { executeCli } from './bootstrap.js';

const workspaceRoot = fileURLToPath(new URL('../../../../', import.meta.url));
let isolatedHome: string;
let fixtureProjectId: string;
const originalEnvironment = {
  HOME: process.env.HOME,
  KB_HOME: process.env.KB_HOME,
  KB_PLATFORM_ROOT: process.env.KB_PLATFORM_ROOT,
  KB_PROJECT_ROOT: process.env.KB_PROJECT_ROOT,
  KB_NET_OFFSET: process.env.KB_NET_OFFSET,
  KB_LOG_LEVEL: process.env.KB_LOG_LEVEL,
  KB_LOG_STREAM: process.env.KB_LOG_STREAM,
};
const originalFetch = globalThis.fetch;
const logger = {
  trace(message: string) { process.stderr.write(`[trace] ${message}\n`); },
  debug(message: string) { process.stderr.write(`[debug] ${message}\n`); },
  info(message: string) { process.stderr.write(`[info] ${message}\n`); },
  warn(message: string) { process.stderr.write(`[warn] ${message}\n`); },
  error(message: string) { process.stderr.write(`[error] ${message}\n`); },
  fatal(message: string) { process.stderr.write(`[fatal] ${message}\n`); },
  child() { return logger; },
};
const platform = mocks.platform;

type SystemCommandFixture = Extract<RouteResult, { type: 'system-cmd' }>['cmd'];

function commandPaths(): { segments: string[]; kind: 'system' | 'plugin'; system?: SystemCommandFixture }[] {
  const queue = registry.complete([]).map((entry) => entry.split(' '));
  const commands = new Map<string, { kind: 'system' | 'plugin'; system?: SystemCommandFixture }>();
  while (queue.length > 0) {
    const segments = queue.shift()!;
    const route = registry.resolve(segments);
    if (route.type === 'system-cmd') {
      commands.set(segments.join(' '), { kind: 'system', system: route.cmd });
      continue;
    }
    if (route.type === 'command') {
      commands.set(route.command.manifest.segments.join(' '), { kind: 'plugin' });
      continue;
    }
    const children = route.type === 'system-group'
      ? route.group.commands.map((command) => command.name)
      : route.type === 'group' ? route.childKeys : [];
    for (const child of children) queue.push([...segments, child]);
  }
  return [...commands].sort(([left], [right]) => left.localeCompare(right))
    .map(([command, info]) => ({ segments: command.split(' '), ...info }));
}

describe('JSON envelope coverage for the live CLI registry', () => {
  beforeAll(async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-json-registry-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_PROJECT_ROOT;
    delete process.env.KB_NET_OFFSET;
    process.env.KB_LOG_LEVEL = 'silent';
    process.env.KB_LOG_STREAM = 'stderr';
    globalThis.fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true, data: { webhooks: [] } }) }) as typeof fetch;
    const projects = await createProjectRegistry({ env: process.env });
    fixtureProjectId = (await projects.add(workspaceRoot)).project.id;
    const platformConfig = {
      platform,
      platformConfig: {},
      rawConfig: {},
      rawPlatformConfig: {},
      effectiveConfig: {},
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
      sameLocation: true,
      sources: { roots: { platform: 'env', project: 'platform' }, fields: {} },
    };
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mocks.initializePlatform.mockImplementation(async (config) => ({ ...config, platform }));
    mocks.resolveProjectConfig.mockResolvedValue({
      platformConfig: { adapters: {} },
      rawConfig: { gateway: { access: { mode: 'local' } } },
      rawPlatformConfig: {},
      effectiveConfig: {},
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
      sameLocation: true,
      sources: { roots: { platform: 'env', project: 'registry' }, fields: {} },
    });
    registerBuiltinSystemCommands();
    await registerBuiltinCommands({
      cwd: workspaceRoot,
      env: process.env,
      logger,
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
    });
  });

  afterAll(async () => {
    mocks.initializePlatform.mockReset();
    globalThis.fetch = originalFetch;
    await rm(isolatedHome, { recursive: true, force: true });
    for (const [key, value] of Object.entries(originalEnvironment)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });

  // case: S-203.1, S-203.2
  it('checks declared safe-valid and invalid-input paths for every system command', async () => {
    const commands = commandPaths();
    expect(commands.some((command) => command.kind === 'system')).toBe(true);
    expect(commands.length).toBeGreaterThan(0);

    const fixtureManifest = path.join(isolatedHome, 'fixture-manifest.json');
    const fixtureManifestContent = JSON.stringify({
      manifestVersion: 3,
      id: '@fixture/registry-test',
      version: '1.0.0',
      displayName: 'Registry test fixture',
      description: 'Read-only CLI JSON coverage fixture',
      cli: { commands: [] },
    });
    const { writeFile } = await import('node:fs/promises');
    await writeFile(fixtureManifest, fixtureManifestContent, 'utf8');

    const runAndCapture = async (argv: string[]) => {
      const stdoutConsole = [
        vi.spyOn(console, 'log').mockImplementation(() => {}),
        vi.spyOn(console, 'info').mockImplementation(() => {}),
        vi.spyOn(console, 'debug').mockImplementation(() => {}),
      ];
      const stdout = vi.spyOn(process.stdout, 'write').mockImplementation(() => true);
      const stderr = vi.spyOn(process.stderr, 'write').mockImplementation(() => true);
      const stderrConsole = [
        vi.spyOn(console, 'warn').mockImplementation((...args) => {
          process.stderr.write(`${args.map(String).join(' ')}\n`);
        }),
        vi.spyOn(console, 'error').mockImplementation((...args) => {
          process.stderr.write(`${args.map(String).join(' ')}\n`);
        }),
        vi.spyOn(console, 'trace').mockImplementation((...args) => {
          process.stderr.write(`${args.map(String).join(' ')}\n`);
        }),
      ];
      try {
        const exitCode = await executeCli(argv, {
          cwd: workspaceRoot,
          env: process.env,
          registry,
          registerBuiltinCommands: () => {},
        });
        const stdoutRecords = [
          ...stdoutConsole.flatMap((spy) => spy.mock.calls.map((args) => args.map(String).join(' '))),
          ...stdout.mock.calls.map(([record]) => String(record)),
        ];
        const stderrRecords = stderr.mock.calls.map(([record]) => String(record));
        expect(stdoutRecords, argv.join(' ')).toHaveLength(1);
        expect(stderrRecords.every((record) => typeof record === 'string'), argv.join(' ')).toBe(true);
        return {
          exitCode,
          result: JSON.parse(stdoutRecords[0]!) as Record<string, unknown>,
          stdoutRecords,
          stderrRecords,
        };
      } finally {
        for (const spy of stdoutConsole) spy.mockRestore();
        stdout.mockRestore();
        stderr.mockRestore();
        for (const spy of stderrConsole) spy.mockRestore();
      }
    };

    for (const command of commands) {
      if (command.kind === 'system') {
        const system = command.system!;
        if ('safeArgs' in system.jsonTest) {
          expect(system.jsonTest.safeArgs.every((argument) => typeof argument === 'string'), command.segments.join(' ')).toBe(true);
          const safeArgs = system.jsonTest.safeArgs.map((argument) => {
            if (argument === '__fixture_project_id__') return fixtureProjectId;
            if (argument === '__fixture_manifest__') return fixtureManifest;
            if (argument.startsWith('__fixture_')) return `${argument}-id`;
            return argument;
          });
          const valid = await runAndCapture([...command.segments, ...safeArgs, '--json']);
          expect(valid.exitCode, `${command.segments.join(' ')}: ${JSON.stringify(valid.result)}`).toBe(0);
          expect(valid.result).toMatchObject({ ok: true, data: expect.anything() });
          expect(valid.stderrRecords.length, command.segments.join(' ')).toBeGreaterThan(0);
        } else {
          expect(system.jsonTest.noSafeValidPath.trim(), command.segments.join(' ')).not.toBe('');
          const help = await runAndCapture([...command.segments, '--help', '--json']);
          expect(help.exitCode, command.segments.join(' ')).toBe(0);
          expect(help.result).toMatchObject({ ok: true });
        }

        const invalid = await runAndCapture([
          ...command.segments,
          '--__kb_registry_invalid_fixture__',
          '--json',
        ]);
        expect(invalid.exitCode, command.segments.join(' ')).toBe(2);
        expect(invalid.result).toMatchObject({
          ok: false,
          error: {
            code: expect.stringMatching(/^KB_/),
            hint: expect.any(String),
          },
        });
      } else {
        const validHelp = await runAndCapture([...command.segments, '--help', '--json']);
        expect(validHelp.exitCode, command.segments.join(' ')).toBe(0);
        expect(validHelp.result).toMatchObject({ ok: true });
        const invalidSegments = [...command.segments];
        invalidSegments[invalidSegments.length - 1] += '__unknown__';
        const invalid = await runAndCapture([...invalidSegments, '--json']);
        expect(invalid.exitCode, invalidSegments.join(' ')).toBe(2);
        expect(invalid.result).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID' } });
      }
    }
  }, 120_000);
});
