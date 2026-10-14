import {
  createJsonPresenter,
  createTextPresenter,
  createContext,
  parseArgs,
  registerSecretFlagValues,
  type ExecutionLimits,
} from "@kb-labs/cli-runtime";
import {
  registerBuiltinCommands,
  renderGlobalHelp,
  renderGroupHelp,
  renderManifestCommandHelp,
  renderProductHelp,
  registry,
  getDiscoveryDiagnostics,
  registerBuiltinSystemCommands,
  generateCommandSchema,
  type RegisteredCommand,
} from "@kb-labs/cli-commands";
import type { Presenter } from "@kb-labs/cli-runtime";
import { createOutput } from "@kb-labs/core-sys/output";
import {
  createCliRuntime,
  type RuntimeSetupOptions,
  type MiddlewareConfig,
  type OutputFormatter,
} from "@kb-labs/cli-runtime";
import { handleLimitFlag } from "./limits";
import { getDefaultMiddlewares } from "./middlewares";
import { executePlugin, createSystemCommandContext } from "./plugin-executor";
import { tryResolveGateway, executeViaGateway, pluginCommandUnavailable } from "./gateway-executor";
import { loadEnvFile } from "./env-loader";
import { initializePlatform, loadPlatformConfiguration, resolveProjectConfig } from "./platform-init";
import { resolveVersion } from "./helpers/version";
import { shouldShowLimits } from "./helpers/flags";
import { hasNestedCommands } from "./help-routing";
import type { PlatformContainer, PlatformConfig } from "@kb-labs/core-runtime";
import { createContextLogger, createErrorEnvelope, createProcessRootLogContext, toErrorEnvelope } from "@kb-labs/core-platform";
import { isProjectRegistryError, projectErrorEnvelope } from "@kb-labs/core-project-registry";
import { hasUsablePlatformAuth, resolveActiveGateway } from "@kb-labs/cli-runtime/gateway";
import { dispatchWithPlatformAuthGate } from "./platform-auth-gate";
import { isPlatformAuthExempt, isPlatformAuthExemptFlag } from "./platform-auth-policy";
import { resolveProjectScope } from "./command-scope";
import { handleExecutionError } from "./execution-error";

declare global {
  var __KB_PLATFORM_CONFIG__: PlatformConfig | undefined;
  var __KB_RAW_CONFIG__: Record<string, unknown> | undefined;
  var __KB_EFFECTIVE_CONFIG__: Record<string, unknown> | undefined;
}

type ILogger = PlatformContainer["logger"];

type RuntimeInitOptions = RuntimeSetupOptions;
type CliRuntimeInstance = Awaited<ReturnType<typeof createCliRuntime>>;
type CliExecutionContext = CliRuntimeInstance["context"];

export async function configuredGatewayUrl(cwd = process.cwd(), platformRoot?: string) {
  return resolveActiveGateway({ cwd, env: process.env, platformRoot });
}

export function createUnknownCommandError(
  input: string,
  suggestions: string[] = [],
  discoveryDiagnostics = getDiscoveryDiagnostics(),
) {
  const discoveryFailures = discoveryDiagnostics
    .filter(event => event.severity === 'error' && event.context?.pluginId)
    .map(event => ({ pluginId: event.context!.pluginId!, code: event.code, remediation: event.remediation }));
  const failedPlugins = [...new Map(discoveryFailures.map(item => [`${item.pluginId}:${item.code}`, item])).values()];
  const remediation = [...new Set(failedPlugins.flatMap(item => item.remediation ? [item.remediation] : []))];
  const nextStep = remediation.length > 0
    ? remediation.join(' ')
    : 'Run "kb marketplace plugins doctor --scope platform".';
  return createErrorEnvelope("KB_RUNTIME_INPUT_INVALID", {
    message: `Unknown command: ${input}`,
    hint: failedPlugins.length > 0
      ? `Enabled plugin ${failedPlugins.map(item => `${item.pluginId} (${item.code})`).join(', ')} failed to load. ${nextStep} Then retry.`
      : "Check the command name or run \"kb --help\" to list available commands.",
    details: {
      command: input,
      ...(suggestions.length > 0 ? { suggestions: suggestions.join(", ") } : {}),
      ...(failedPlugins.length > 0 ? {
        pluginDiagnostics: failedPlugins.map(item => `${item.pluginId}:${item.code}`).join(', '),
      } : {}),
    },
  });
}
export interface CliRuntimeOptions {
  env?: NodeJS.ProcessEnv;
  version?: string;
  cwd?: string;
  moduleUrl?: string;
  registerBuiltinCommands?: (input: {
    cwd: string;
    env: NodeJS.ProcessEnv;
    platformRoot?: string;
    projectRoot?: string;
  }) => Promise<void> | void;
  parseArgs?: typeof parseArgs;
  registry?: typeof registry;
  createJsonPresenter?: typeof createJsonPresenter;
  createTextPresenter?: typeof createTextPresenter;
  createRuntime?: (options: RuntimeInitOptions) => Promise<CliRuntimeInstance>;
  createRuntimeContext?: (
    options: RuntimeInitOptions,
  ) => Promise<CliExecutionContext>;
  runtimeExecutionLimits?: ExecutionLimits;
  runtimeMiddlewares?: MiddlewareConfig[];
  runtimeFormatters?: OutputFormatter[];
}

// eslint-disable-next-line sonarjs/cognitive-complexity
export async function executeCli(
  argv: string[],
  options: CliRuntimeOptions = {},
): Promise<number | void> {
  const cwd = options.cwd ?? process.cwd();
  const parse = options.parseArgs ?? parseArgs;
  const { cmdPath, rest, global, flagsObj } = parse(argv);
  createProcessRootLogContext({
    applicationId: 'kb',
    serviceId: 'kb',
    instanceId: `${process.pid}`,
    layer: 'cli',
  });
  const registryStore = options.registry ?? registry;
  registerBuiltinSystemCommands();
  const jsonPresenterFactory = options.createJsonPresenter ?? createJsonPresenter;
  const textPresenterFactory = options.createTextPresenter ?? createTextPresenter;

  // Logger destinations are selected while platform adapters are created.
  // Establish the CLI boundary before initialization for programmatic callers too.
  process.env.KB_LOG_STREAM = "stderr";
  if (global.json) { process.env.KB_OUTPUT_MODE = "json"; }

  loadEnvFile(cwd);

  let loadedConfig: Awaited<ReturnType<typeof loadPlatformConfiguration>>;
  try {
    loadedConfig = await loadPlatformConfiguration(cwd, options.moduleUrl, 'platform');
  } catch (error) {
    return reportInitializationError(error, global, jsonPresenterFactory, textPresenterFactory);
  }
  globalThis.__KB_RAW_CONFIG__ = loadedConfig.rawConfig;
  const authRoute = registryStore.resolve(cmdPath);
  const authCommandKind = authRoute.type === "system-cmd" ? "system" : "plugin";
  const authCommandPath = authRoute.type === "system-cmd"
    ? authRoute.cmd.category ? `${authRoute.cmd.category} ${authRoute.cmd.name}` : authRoute.cmd.name
    : cmdPath.join(" ");
  const authExempt = isPlatformAuthExempt(authCommandPath, authCommandKind)
    || isPlatformAuthExemptFlag("help") && global.help
    || isPlatformAuthExemptFlag("version") && global.version;
  const accessMode = resolveCliAccessMode();
  let gatewayTarget: Awaited<ReturnType<typeof configuredGatewayUrl>> | undefined;
  if (accessMode === 'secured' && !authExempt) {
    try {
      gatewayTarget = await configuredGatewayUrl(cwd, loadedConfig.platformRoot);
    } catch (error) {
      return reportInitializationError(error, global, jsonPresenterFactory, textPresenterFactory);
    }
  }
  const authCheck: Awaited<ReturnType<typeof hasUsablePlatformAuth>> = accessMode === 'secured' && !authExempt
    ? await hasUsablePlatformAuth(gatewayTarget)
    : { authenticated: true };
  const authResult = global.version && isPlatformAuthExemptFlag("version")
    ? { value: true }
    : await dispatchWithPlatformAuthGate({
      accessMode,
      commandPath: authCommandPath,
      commandKind: authCommandKind,
      hasAuth: authCheck.authenticated,
      ...(authCheck.error ? { authError: authCheck.error } : {}),
      gatewayUrl: gatewayTarget?.gatewayUrl,
      help: global.help,
    }, async () => true);
  if ("error" in authResult) {
    const presenter = global.json ? jsonPresenterFactory() : textPresenterFactory(global.quiet);
    return reportGateResult(authResult, global.json, presenter);
  }

  const projectScope = resolveProjectScope(authRoute, global, cmdPath, flagsObj);
  if (projectScope === "required") {
    try {
      loadedConfig = await resolveProjectConfig(cwd, options.moduleUrl);
    } catch (error) {
      return reportInitializationError(error, global, jsonPresenterFactory, textPresenterFactory);
    }
  }

  const isGlobalHelp = global.help && isPlatformAuthExemptFlag('help') && cmdPath.length === 0;
  const isGlobalVersion = global.version && isPlatformAuthExemptFlag('version');
  let initialized: Awaited<ReturnType<typeof initializePlatform>>;
  try {
    initialized = await initializePlatform(loadedConfig, {
      skipAdapterInitialization: Boolean(isGlobalHelp || isGlobalVersion),
    });
  } catch (error) {
    return reportInitializationError(error, global, jsonPresenterFactory, textPresenterFactory);
  }

  const { platform, platformConfig, rawConfig, effectiveConfig, platformRoot, projectRoot } = initialized;

  if (platformRoot) {
    loadEnvFile(platformRoot);
  }
  if (projectRoot && projectRoot !== cwd) {
    loadEnvFile(projectRoot);
  }

  globalThis.__KB_PLATFORM_CONFIG__ = platformConfig;
  globalThis.__KB_RAW_CONFIG__ = rawConfig;
  globalThis.__KB_EFFECTIVE_CONFIG__ = effectiveConfig ?? rawConfig;

  if (rawConfig) {
    process.env.KB_RAW_CONFIG_JSON = JSON.stringify(rawConfig);
  }

  const env = options.env ?? process.env;
  const version = resolveVersion(options.version, env);
  const runtimeFactory =
    options.createRuntime ??
    (async (runtimeOptions: RuntimeInitOptions) => {
      const providedContext = options.createRuntimeContext
        ? await options.createRuntimeContext(runtimeOptions)
        : undefined;
      return createCliRuntime({ ...runtimeOptions, context: providedContext });
    });

  applyLogLevel(global.debug, global.logLevel, env.KB_LOG_LEVEL);

  const cliLogger = createCliLogger(
    platform,
    cwd,
    version,
    argv,
    cmdPath,
    global,
  );

  const registerCommands =
    options.registerBuiltinCommands ?? registerBuiltinCommands;
  await registerCommands({
    cwd,
    env,
    logger: cliLogger,
    platformRoot,
    projectRoot,
  });

  // ── Handle --version early ─────────────────────────────────────────────────
  if (global.version && isPlatformAuthExemptFlag('version')) {
    const presenter = global.json ? jsonPresenterFactory() : textPresenterFactory(global.quiet);
    if (global.json) {
      presenter.json({ ok: true, version });
    } else {
      presenter.write(version);
    }
    return 0;
  }

  // ── Handle --help with no command → global help ────────────────────────────
  if (global.help && isPlatformAuthExemptFlag('help') && cmdPath.length === 0) {
    const presenter = global.json ? jsonPresenterFactory() : textPresenterFactory(global.quiet);
    if (global.json) {
      presenter.json({
        ok: false,
        error: createErrorEnvelope("KB_RUNTIME_INPUT_INVALID", {
          message: "Help is only available in text mode.",
          hint: "Run \"kb --help\" without --json to list commands.",
        }),
      });
    } else {
      presenter.write(renderGlobalHelp(registryStore));
    }
    return global.json ? 2 : 0;
  }

  // ── Handle __complete (shell tab completion) ───────────────────────────────
  if (cmdPath[0] === "__complete") {
    const completionTokens = cmdPath.slice(1);
    const completions = registryStore.complete(completionTokens);
    for (const c of completions) {
      process.stdout.write(c + "\n");
    }
    return 0;
  }

  const presenter = global.json
    ? jsonPresenterFactory()
    : textPresenterFactory(global.quiet);

  const output = createOutput({
    verbosity: global.quiet ? "quiet" : global.debug ? "debug" : "normal",
    json: global.json,
    format: global.debug ? "human" : "human",
    category: "cli",
  });

  const logger = cliLogger.child({
    category: "bootstrap",
    meta: { service: "runtime-bootstrap" },
  });

  logger.debug("[bootstrap] rawConfig", {
    state: rawConfig ? "EXISTS" : "UNDEFINED",
  });
  if (rawConfig) {
    logger.debug("[bootstrap] rawConfig keys", {
      keys: Object.keys(rawConfig),
    });
  }

  const runtimeMiddlewares =
    options.runtimeMiddlewares ?? getDefaultMiddlewares();

  const cliContext = await createContext({
    presenter,
    logger,
    output,
    env,
    cwd,
    profileId: global.profile,
    verbosity: global.quiet ? "quiet" : global.verbose ? "verbose" : "normal",
    jsonMode: global.json || false,
  });

  const runtimeInitOptions: RuntimeInitOptions = {
    presenter,
    output,
    env,
    cwd,
    context: cliContext,
    executionLimits: options.runtimeExecutionLimits,
    middlewares: runtimeMiddlewares,
    formatters: options.runtimeFormatters,
  };

  // ── Handle limit flags ─────────────────────────────────────────────────────
  if (shouldShowLimits(flagsObj)) {
    return handleLimitFlag({
      cmdPath,
      presenter,
      registry: registryStore,
      asJson: Boolean(global.json),
    });
  }

  // ── Route using Trie ───────────────────────────────────────────────────────
  const routeResult = registryStore.resolve(cmdPath);
  const commandFlags = routeResult.type === 'system-cmd'
    ? routeResult.cmd.flags ?? []
    : routeResult.type === 'command'
      ? routeResult.command.manifest.flags ?? []
      : [];
  registerSecretFlagValues(commandFlags, flagsObj);

  switch (routeResult.type) {
    case "not-found": {
      const input = routeResult.input.join(" ") || "(none)";
      const diagCmd = `kb diag --command "${input}"`;
      const unknownCommand = createUnknownCommandError(input, routeResult.suggestions);
      const diagHint = unknownCommand.hint !== 'Check the command name or run "kb --help" to list available commands.'
        ? `\n${unknownCommand.hint}`
        : `\nTip: run  ${diagCmd}  to diagnose`;
      if (routeResult.suggestions.length > 0) {
        const sugs = routeResult.suggestions.map((s) => `  kb ${s}`).join("\n");
        if (global.json) {
          presenter.json({ ok: false, error: unknownCommand });
        } else {
          presenter.error(
            `Unknown command: ${input}\nDid you mean:\n${sugs}${diagHint}`,
          );
        }
      } else {
        if (global.json) {
          presenter.json({ ok: false, error: unknownCommand });
        } else {
          presenter.error(`Unknown command: ${input}${diagHint}`);
        }
      }
      return 2;
    }

    case "ambiguous": {
      const candidates = routeResult.candidates
        .map((c) => `  kb ${c}`)
        .join("\n");
      if (global.json) {
        presenter.json({
          ok: false,
          error: createErrorEnvelope("KB_RUNTIME_INPUT_INVALID", {
            message: "The command name is ambiguous.",
            hint: `Choose one of: ${routeResult.candidates.map((candidate) => `kb ${candidate}`).join(", ")}`,
            details: { candidates: routeResult.candidates.join(", ") },
          }),
        });
      } else {
        presenter.error(`Ambiguous command. Did you mean:\n${candidates}`);
      }
      return 2;
    }

    case "group": {
      const { segments, describe, childKeys } = routeResult;
      if (global.json) {
        presenter.json({
          ok: true,
          group: { path: segments.join(" "), describe, children: childKeys },
        });
        return 0;
      }
      // Try to get detailed commands for the group
      const groupCommands = registryStore.listCommandsUnder(segments);
      if (groupCommands.length > 0) {
        presenter.write(renderProductHelp(segments.join(" "), groupCommands));
      } else {
        presenter.write(renderGroupHelp({ segments, describe, childKeys }));
      }
      return 0;
    }

    case "system-group": {
      const group = routeResult.group;
      if (global.json) {
        presenter.json({
          ok: true,
          group: {
            name: group.name,
            describe: group.describe,
            commands: group.commands.map((c) => ({
              name: c.name,
              describe: c.describe,
            })),
          },
        });
        return 0;
      }
      presenter.write(
        renderGroupHelp({
          segments: [group.name],
          describe: group.describe,
          childKeys: group.commands.map((c) => c.name),
        }),
      );
      return 0;
    }

    case "system-cmd": {
      const cmd = routeResult.cmd;
      const actualRest = routeResult.rest;

      if (global.help && isPlatformAuthExemptFlag('help')) {
        const lines = [`${cmd.name}: ${cmd.describe}`];
        if (cmd.longDescription) {
          lines.push("", cmd.longDescription);
        }
        if (cmd.flags && cmd.flags.length > 0) {
          lines.push("", "Flags:");
          for (const f of cmd.flags) {
            lines.push(`  --${f.name}  ${f.description ?? ""}`);
          }
        }
        if (global.json) {
          presenter.json({ ok: true, help: lines.join("\n") });
        } else {
          presenter.write(lines.join("\n"));
        }
        return 0;
      }

      try {
        const runtime = await runtimeFactory(runtimeInitOptions);
        const v3Context = createSystemCommandContext(cliContext, platform);
        return runtime.middleware.execute(cliContext, async () => {
          const code = await cmd.run(v3Context, actualRest, {
            ...global,
            ...flagsObj,
          });
          return typeof code === "number" ? code : 0;
        });
      } catch (error: unknown) {
        return handleExecutionError(error, presenter);
      }
    }

    case "command": {
      const manifestCmd = routeResult.command;
      const actualRest = routeResult.rest;

      if (flagsObj["schema"] && manifestCmd.manifest.operationType) {
        const schema = generateCommandSchema(manifestCmd.manifest);
        if (global.json) {
          presenter.json({ ok: true, schema });
        } else {
          process.stdout.write(JSON.stringify(schema, null, 2) + "\n");
        }
        return 0;
      }

      if (global.help && isPlatformAuthExemptFlag('help')) {
        if (global.json) {
          presenter.json({
            ok: true,
            command: manifestCmd.manifest.segments.join(" "),
            manifest: {
              describe: manifestCmd.manifest.describe,
              longDescription: manifestCmd.manifest.longDescription,
              flags: manifestCmd.manifest.flags,
              examples: manifestCmd.manifest.examples,
              aliases: manifestCmd.manifest.aliases,
            },
          });
        } else {
          const commandSegments = manifestCmd.manifest.segments;
          const commandsUnderPath = registryStore.listCommandsUnder([
            ...commandSegments,
          ]);

          if (hasNestedCommands(commandSegments, commandsUnderPath)) {
            presenter.write(
              renderProductHelp(commandSegments.join(" "), commandsUnderPath),
            );
          } else {
            presenter.write(renderManifestCommandHelp(manifestCmd));
          }
        }
        return 0;
      }

      try {
        return await dispatchPlugin({
          manifestCmd,
          actualRest,
          global,
          flagsObj,
          cliContext,
          platform,
          netOffset: Number(env.KB_NET_OFFSET) || 0,
          platformRoot,
          presenter,
          runWithMiddleware: async (run) => {
            const runtime = await runtimeFactory(runtimeInitOptions);
            return runtime.middleware.execute(cliContext, run);
          },
        });
      } catch (error: unknown) {
        return handleExecutionError(error, presenter);
      }
    }
  }
}

interface DispatchPluginParams {
  manifestCmd: RegisteredCommand;
  actualRest: string[];
  global: ReturnType<typeof parseArgs>["global"];
  flagsObj: ReturnType<typeof parseArgs>["flagsObj"];
  cliContext: CliExecutionContext;
  platform: PlatformContainer;
  platformRoot?: string;
  netOffset: number;
  presenter: Presenter;
  runWithMiddleware: (run: () => Promise<number>) => Promise<number>;
}

async function dispatchPlugin(p: DispatchPluginParams): Promise<number> {
  const commandPath = p.manifestCmd.manifest.segments.join(" ");
  const commandId = p.manifestCmd.manifest.segments.join(":");

  const rawConfig = globalThis.__KB_RAW_CONFIG__;
  const executionMode = (
    rawConfig?.execution as Record<string, unknown> | undefined
  )?.mode;

  if (p.manifestCmd.manifest.unavailablePlugin) {
    const envelope = createErrorEnvelope('KB_PLUGIN_NOT_FOUND', {
      details: { plugin: p.manifestCmd.packageName ?? p.manifestCmd.manifest.group },
      message: `${p.manifestCmd.packageName ?? p.manifestCmd.manifest.group} is locked in this workspace but its manifest has not been built.`,
      hint: 'Run `pnpm build`, then retry the command.',
    });
    if (p.presenter.isJSON) {
      p.presenter.json({ ok: false, error: envelope });
    } else {
      p.presenter.error(`${envelope.message} [${envelope.code}]\nHint: ${envelope.hint}`);
    }
    return 2;
  }

  return p.runWithMiddleware(async () => {
    if (executionMode !== "in-process") {
      const gatewayClient = await tryResolveGateway();
      if (gatewayClient) {
        return executeViaGateway(gatewayClient, {
          commandId,
          argv: p.actualRest,
          flags: { ...p.global, ...p.flagsObj },
          manifestCmd: p.manifestCmd,
        });
      }
    }

    const pluginExitCode = await executePlugin({
      context: p.cliContext,
      netOffset: p.netOffset,
      platformRoot: p.platformRoot,
      commandId,
      argv: p.actualRest,
      flags: { ...p.global, ...p.flagsObj },
      manifestCmd: p.manifestCmd,
      platform: p.platform,
    });

    if (pluginExitCode !== undefined) {
      return pluginExitCode;
    }

    throw pluginCommandUnavailable(commandPath);
  });
}

function resolveCliAccessMode(): unknown {
  const rawConfig = globalThis.__KB_RAW_CONFIG__;
  const gatewayConfig = rawConfig?.gateway as Record<string, unknown> | undefined;
  const accessConfig = gatewayConfig?.access as Record<string, unknown> | undefined;
  return accessConfig?.mode;
}

function reportGateResult<T>(
  gated: { error: import('@kb-labs/core-platform').ErrorEnvelope } | { value: T },
  json: boolean | undefined,
  presenter: Presenter,
): T | number {
  if ('error' in gated) {
    if (json && typeof presenter.json === 'function') {
      presenter.json({ ok: false, error: gated.error });
    } else {
      presenter.error(`${gated.error.code}: ${gated.error.message} ${gated.error.hint}`);
    }
    return 1;
  }
  return gated.value;
}

function reportInitializationError(
  error: unknown,
  global: ReturnType<typeof parseArgs>["global"],
  jsonPresenterFactory: typeof createJsonPresenter,
  textPresenterFactory: typeof createTextPresenter,
): number {
  const envelope = isProjectRegistryError(error)
    ? projectErrorEnvelope(error)
    : toErrorEnvelope(error);
  const presenter = global.json ? jsonPresenterFactory() : textPresenterFactory(global.quiet);
  if (global.json) {
    presenter.json({ ok: false, error: envelope });
  } else {
    presenter.error(`${envelope.message} [${envelope.code}]`);
    presenter.write(`Hint: ${envelope.hint}\n`);
  }
  return 1;
}

type LogLevel = "trace" | "debug" | "info" | "warn" | "error" | "silent";
const VALID_LOG_LEVELS: LogLevel[] = [
  "trace",
  "debug",
  "info",
  "warn",
  "error",
  "silent",
];

function resolveLogLevel(
  debug: unknown,
  logLevelFlag: unknown,
  envLevel: string | undefined,
): LogLevel {
  if (debug) {
    return "debug";
  }
  const raw = String(logLevelFlag ?? envLevel ?? "silent").toLowerCase();
  return VALID_LOG_LEVELS.includes(raw as LogLevel)
    ? (raw as LogLevel)
    : "silent";
}

function applyLogLevel(
  debug: unknown,
  logLevelFlag: unknown,
  envLevel: string | undefined,
): void {
  const logLevel = resolveLogLevel(debug, logLevelFlag, envLevel);
  if (!process.env.LOG_LEVEL && !process.env.KB_LOG_LEVEL) {
    process.env.KB_LOG_LEVEL = logLevel;
  }
  if (debug) {
    process.env.DEBUG_SANDBOX = "1";
  }
}

function createCliLogger(
  platform: PlatformContainer,
  cwd: string,
  version: string,
  argv: string[],
  cmdPath: string[],
  global: { json?: unknown; quiet?: unknown; debug?: unknown },
): ILogger {
  const requestId = `cli-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
  const logger = createContextLogger(platform.logger, {
    applicationId: "kb",
    serviceId: "kb",
    instanceId: `${process.pid}`,
    layer: "cli",
  })
    .forComponent("cli-command")
    .forOperation("cli.invocation", {
      requestId,
      traceId: `trace-${crypto.randomUUID()}`,
      spanId: `span-${crypto.randomUUID()}`,
    })
    .with({ cwd, version, invocationId: `inv-${crypto.randomUUID()}`, argv });
  logger.info("CLI invocation started", {
    commandPath: cmdPath.join(" "),
    argsCount: argv.length,
    jsonMode: Boolean(global.json),
    quietMode: Boolean(global.quiet),
    debugMode: Boolean(global.debug),
  });
  return logger;
}
