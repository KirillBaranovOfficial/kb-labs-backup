import { beforeAll, describe, expect, it } from 'vitest';
import { errorCatalog } from '@kb-labs/core-platform/error-envelope';
import { createErrorEnvelope, createProductErrorEnvelope, ErrorEnvelopeError, normalizePluginHandlerFailure, type ILogger } from '@kb-labs/core-platform';
import { registry, registerBuiltinCommands, registerBuiltinSystemCommands } from '@kb-labs/cli-commands';
import { parseArgs } from '@kb-labs/cli-runtime';
import { join } from 'node:path';

const workspaceRoot = join(import.meta.dirname, '../../../..');

beforeAll(async () => {
  registerBuiltinSystemCommands();
  await registerBuiltinCommands({ cwd: workspaceRoot, platformRoot: workspaceRoot, projectRoot: workspaceRoot });
});

function commandSnippets(text: string): string[] {
  const quoted = [...text.matchAll(/["'`](kb\s+[^"'`]+)["'`]/g)].map((match) => match[1]!.trim());
  return text.startsWith('kb ') ? [text.trim(), ...quoted] : quoted;
}

function declaredFlags(route: ReturnType<typeof registry.resolve>): Set<string> {
  if (route.type === 'command') return new Set((route.command.manifest.flags ?? []).map((flag) => flag.name));
  if (route.type === 'system-cmd') return new Set((route.cmd.flags ?? []).map((flag) => flag.name));
  return new Set();
}

describe('error catalog recovery commands', () => {
  it('resolves each kb command and its flags against the registered CLI', () => {
    const problems: string[] = [];
    for (const entry of errorCatalog.codes) {
      for (const snippet of commandSnippets(entry.hint)) {
        const tokens = snippet.split(/\s+/).filter(Boolean);
        const parsed = parseArgs(tokens.slice(1));
        const route = registry.resolve(tokens.slice(1));
        if (route.type !== 'command' && route.type !== 'system-cmd') {
          if (parsed.cmdPath.length === 0 && parsed.global.help) continue;
          problems.push(`${entry.code}: command does not resolve: ${snippet}`);
          continue;
        }
        const flags = declaredFlags(route);
        for (const token of tokens.slice(1)) {
          if (!token.startsWith('--')) continue;
          const flag = token.slice(2).split('=')[0]!;
          if (!flags.has(flag) && !Object.hasOwn(parsed.global, flag)) {
            problems.push(`${entry.code}: --${flag} is not declared by ${snippet}`);
          }
        }
      }
    }
    expect(problems).toEqual([]);
  });
});

describe('registered plugin handler failure boundary', () => {
  // case: S-203.2, S-203.3, S-203.6
  it('normalises each failure kind for every live registered plugin command', () => {
    const commands = registry.listCommands().filter((command) => command.available && !command.shadowed);
    expect(commands.length).toBeGreaterThan(0);
    for (const [index, command] of commands.entries()) {
      const pluginId = command.manifest.v3Manifest?.id ?? command.packageName ?? command.manifest.group;
      const commandPath = command.manifest.segments.join(' ');
      const correlationId = `rail-${index}`;
      const records: Array<Record<string, unknown>> = [];
      const logger: ILogger = {
        trace: () => {}, debug: () => {}, info: () => {}, warn: () => {},
        error: (message, error, fields) => records.push({ message, error, ...fields }),
        fatal: (message, error, fields) => records.push({ message, error, ...fields }),
        child: () => logger,
      };
      const context = {
        pluginId, command: commandPath, correlationId, logger,
        logContext: { applicationId: 'test', serviceId: 'execution', instanceId: 'rail', layer: 'test' },
        sensitiveValues: [],
      };
      const typedProduct = createProductErrorEnvelope({
        code: 'RAIL_TYPED_FAILURE', message: 'A typed test failure.', hint: 'Correct the test input.', retryable: false,
      });
      const typedPlatform = createErrorEnvelope('KB_ADAPTER_NOT_CONFIGURED', {
        details: { service: 'llm', adapter: 'platform.adapters.llm', command: 'kb adapter bind llm' },
      });
      expect(normalizePluginHandlerFailure(new ErrorEnvelopeError(typedProduct), context)).toMatchObject({
        code: 'RAIL_TYPED_FAILURE', correlationId,
      });
      expect(normalizePluginHandlerFailure(new ErrorEnvelopeError(typedPlatform), context)).toMatchObject({
        code: 'KB_ADAPTER_NOT_CONFIGURED', correlationId,
      });
      expect(records).toHaveLength(0);

      for (const [failure, reason] of [
        [new Error('unexpected test failure'), 'failed unexpectedly'],
        [Object.assign(new Error('timed out'), { name: 'TimeoutError', code: 'TIMEOUT' }), 'timed out'],
        [Object.assign(new Error('worker crashed'), { name: 'WorkerCrashedError', code: 'WORKER_CRASHED' }), 'execution process crashed'],
      ] as const) {
        const envelope = normalizePluginHandlerFailure(failure, context);
        expect(envelope).toMatchObject({ code: 'KB_PLUGIN_COMMAND_FAILED', correlationId });
        expect(envelope.message).toContain(reason);
        expect(records.at(-1)).toMatchObject({ code: 'KB_PLUGIN_COMMAND_FAILED', correlationId, failureReason: reason });
      }

      const malformed = normalizePluginHandlerFailure({ code: 'RAIL_MISSING_HINT', message: 'Malformed', retryable: false }, context);
      expect(malformed).toMatchObject({
        code: 'KB_PLUGIN_CONTRACT_VIOLATION', correlationId,
        message: expect.stringContaining(pluginId), details: { field: 'hint' },
      });
      expect(records).toHaveLength(4);
      expect(records.map((record) => record.correlationId)).toEqual(Array(4).fill(correlationId));
    }
  });
});
