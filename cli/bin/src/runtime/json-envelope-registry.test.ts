import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { registerBuiltinCommands, registerBuiltinSystemCommands, registry } from '@kb-labs/cli-commands';

const mocks = vi.hoisted(() => ({ initializePlatform: vi.fn() }));

vi.mock('./platform-init.js', () => ({ initializePlatform: mocks.initializePlatform }));

import { executeCli } from './bootstrap.js';

const workspaceRoot = fileURLToPath(new URL('../../../../', import.meta.url));
let isolatedHome: string;
const originalEnvironment = {
  HOME: process.env.HOME,
  KB_HOME: process.env.KB_HOME,
  KB_PLATFORM_ROOT: process.env.KB_PLATFORM_ROOT,
  KB_PROJECT_ROOT: process.env.KB_PROJECT_ROOT,
  KB_NET_OFFSET: process.env.KB_NET_OFFSET,
  KB_LOG_LEVEL: process.env.KB_LOG_LEVEL,
  KB_LOG_STREAM: process.env.KB_LOG_STREAM,
};
const logger = {
  trace() {}, debug() {}, info() {}, warn() {}, error() {}, fatal() {},
  child() { return logger; },
};
const platform = { logger };

function commandPaths(): { segments: string[]; kind: 'system' | 'plugin' }[] {
  const queue = registry.complete([]).map((entry) => entry.split(' '));
  const commands = new Map<string, 'system' | 'plugin'>();
  while (queue.length > 0) {
    const segments = queue.shift()!;
    const route = registry.resolve(segments);
    if (route.type === 'system-cmd') {
      commands.set(segments.join(' '), 'system');
      continue;
    }
    if (route.type === 'command') {
      commands.set(route.command.manifest.segments.join(' '), 'plugin');
      continue;
    }
    const children = route.type === 'system-group'
      ? route.group.commands.map((command) => command.name)
      : route.type === 'group' ? route.childKeys : [];
    for (const child of children) queue.push([...segments, child]);
  }
  return [...commands].sort(([left], [right]) => left.localeCompare(right))
    .map(([command, kind]) => ({ segments: command.split(' '), kind }));
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
    mocks.initializePlatform.mockResolvedValue({
      platform,
      platformConfig: {},
      rawConfig: {},
      effectiveConfig: {},
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
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
    await rm(isolatedHome, { recursive: true, force: true });
    for (const [key, value] of Object.entries(originalEnvironment)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });

  // case: S-203.1, S-203.2
  it('checks safe help and invalid-route output for every registered system and plugin command', async () => {
    const commands = commandPaths();
    expect(commands.some((command) => command.kind === 'system')).toBe(true);
    expect(commands.some((command) => command.kind === 'plugin')).toBe(true);
    expect(commands.length).toBeGreaterThan(0);

    const env = process.env;
    const runAndCapture = async (argv: string[]) => {
      const output = vi.spyOn(console, 'log').mockImplementation(() => {});
      try {
        const exitCode = await executeCli(argv, {
          cwd: workspaceRoot,
          env,
          registry,
          registerBuiltinCommands: () => {},
        });
        const records = output.mock.calls.map(([record]) => String(record));
        expect(records, argv.join(' ')).toHaveLength(1);
        return { exitCode, result: JSON.parse(records[0]!) as Record<string, unknown> };
      } finally {
        output.mockRestore();
      }
    };

    for (const command of commands) {
      const valid = await runAndCapture([...command.segments, '--help', '--json']);
      expect(valid.exitCode, command.segments.join(' ')).toBe(0);
      expect(valid.result).toMatchObject({ ok: true, data: expect.any(Object) });

      const invalidSegments = [...command.segments];
      invalidSegments[invalidSegments.length - 1] += '__unknown__';
      const invalid = await runAndCapture([...invalidSegments, '--json']);
      expect(invalid.exitCode, invalidSegments.join(' ')).toBe(2);
      expect(invalid.result).toMatchObject({
        ok: false,
        error: {
          code: 'KB_RUNTIME_INPUT_INVALID',
          hint: expect.any(String),
        },
      });
    }
  }, 120_000);
});
