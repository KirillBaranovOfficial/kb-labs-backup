import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const workspaceRoot = fileURLToPath(new URL('../../../../', import.meta.url));

describe('JSON envelope coverage for the live CLI registry', () => {
  let isolatedHome: string | undefined;

  afterEach(async () => {
    if (isolatedHome) { await rm(isolatedHome, { recursive: true, force: true }); }
    isolatedHome = undefined;
  });

  // case: S-203.1
  it('walks every registered system and plugin command and parses one JSON envelope per command', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-json-registry-'));
    const script = `
      import { pathToFileURL } from 'node:url';
      import path from 'node:path';
      const { registry, registerBuiltinCommands } = await import(
        pathToFileURL(path.resolve('cli/commands/dist/index.js')).href
      );

      await registerBuiltinCommands({ cwd: process.cwd(), env: process.env });
      const { run } = await import(pathToFileURL(path.resolve('cli/bin/dist/index.js')).href);
      const queue = registry.complete([]).map((entry) => entry.split(' '));
      const commands = new Set();
      let systemCount = 0;
      let pluginCount = 0;

      while (queue.length > 0) {
        const segments = queue.shift();
        const route = registry.resolve(segments);
        if (route.type === 'system-cmd') {
          commands.add(segments.join(' '));
          systemCount += 1;
          continue;
        }
        if (route.type === 'command') {
          commands.add(route.command.manifest.segments.join(' '));
          pluginCount += 1;
          continue;
        }
        const children = route.type === 'system-group'
          ? route.group.commands.map((command) => command.name)
          : route.type === 'group' ? route.childKeys : [];
        for (const child of children) { queue.push([...segments, child]); }
      }

      const originalWrite = process.stdout.write.bind(process.stdout);
      const failures = [];
      for (const command of [...commands].sort()) {
        let stdout = '';
        process.stdout.write = (chunk) => { stdout += String(chunk); return true; };
        try {
          const exitCode = await run([...command.split(' '), '--help', '--json']);
          const records = stdout.trim().split(/\\r?\\n/).filter(Boolean);
          const result = records.length === 1 ? JSON.parse(records[0]) : undefined;
          const keys = result && typeof result === 'object' ? Object.keys(result).sort() : [];
          if (exitCode !== 0 || result?.ok !== true || !('data' in result) ||
              keys.some((key) => !['data', 'meta', 'ok'].includes(key))) {
            failures.push({ command, exitCode, stdout });
          }
        } catch (error) {
          failures.push({ command, stdout, error: error instanceof Error ? error.message : String(error) });
        } finally {
          process.stdout.write = originalWrite;
        }
      }
      originalWrite(JSON.stringify({ systemCount, pluginCount, commandCount: commands.size, failures }) + '\\n');
      if (failures.length > 0 || commands.size === 0 || systemCount === 0 || pluginCount === 0) {
        process.exitCode = 1;
      }
    `;
    const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], {
      cwd: workspaceRoot,
      encoding: 'utf8',
      timeout: 120_000,
      env: {
        ...process.env,
        HOME: isolatedHome,
        KB_HOME: path.join(isolatedHome, '.kb'),
        KB_LOG_LEVEL: 'silent',
        KB_LOG_STREAM: 'stderr',
      },
    });

    expect(result.status, result.stderr || result.stdout).toBe(0);
    const summary = JSON.parse(result.stdout) as {
      systemCount: number;
      pluginCount: number;
      commandCount: number;
      failures: Array<{ command: string }>;
    };
    expect(summary.systemCount).toBeGreaterThan(0);
    expect(summary.pluginCount).toBeGreaterThan(0);
    expect(summary.commandCount).toBeGreaterThan(0);
    expect(summary.failures).toEqual([]);
  });
});
