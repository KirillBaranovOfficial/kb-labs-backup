/**
 * V3 CLI Command Execution
 *
 * Entry point for executing plugin commands via V3 plugin system.
 * Uses unified platform.executionBackend for all execution.
 */

import type {
  PluginContextDescriptor,
  UIFacade,
  PlatformServices,
} from "@kb-labs/plugin-contracts";
import { wrapCliResult } from "@kb-labs/plugin-runtime";
import { createErrorEnvelope, getCatalogEntry } from "@kb-labs/core-platform";
import type { PlatformContainer } from "@kb-labs/core-runtime";
import type { ExecutionRequest } from "@kb-labs/plugin-execution-factory";
import * as path from "node:path";
import { randomUUID } from "node:crypto";

export interface ExecuteCommandV3Options {
  /** Explicit plugin environment values for this invocation. */
  env?: Record<string, string | undefined>;
  /**
   * Plugin ID (e.g., "@kb-labs/my-plugin")
   */
  pluginId: string;

  /**
   * Plugin version
   */
  pluginVersion: string;

  /**
   * Path to command handler file
   */
  handlerPath: string;

  /**
   * Absolute plugin root directory. When the caller already knows where
   * the plugin lives (e.g. from a discovery result's `pkgRoot`), it should
   * pass it here — module resolution inside the execution backend will
   * then search the plugin's own `node_modules`, which is required for
   * project-scope plugins whose dependencies live at
   * `<projectRoot>/.kb/plugins/<name>/node_modules/`.
   *
   * If omitted, a fallback resolver walks `node_modules` starting from
   * `cwd` (legacy behaviour — only works for plugins installed at the
   * CLI invocation directory).
   */
  pluginRoot?: string;

  /**
   * Command arguments
   */
  argv: string[];

  /**
   * Command flags
   */
  flags: Record<string, unknown>;

  /**
   * Plugin configuration
   */
  config?: unknown;

  /**
   * Plugin permissions
   */
  permissions?: PluginContextDescriptor["permissions"];

  /**
   * Working directory
   */
  cwd?: string;

  /**
   * Output directory
   */
  outdir?: string;

  /**
   * Tenant ID
   */
  tenantId?: string;

  /**
   * UI facade for user interaction
   */
  ui: UIFacade;

  /**
   * Platform services
   */
  platform: PlatformServices;

  /**
   * Platform container (for executionBackend access - internal use only)
   */
  platformContainer: PlatformContainer;

  /**
   * Abort signal
   */
  signal?: AbortSignal;

  /**
   * Development mode (runs in-process for easier debugging)
   */
  devMode?: boolean;

  /**
   * Unix socket path for IPC communication.
   * If not provided, will attempt to get from platform.getSocketPath()
   */
  socketPath?: string;

  /**
   * Resource quotas from manifest
   */
  quotas?: {
    timeoutMs?: number;
    memoryMb?: number;
    cpuMs?: number;
  };

  /**
   * Config section identifier from manifest (for useConfig auto-detection)
   */
  configSection?: string;
}

/**
 * Execute a plugin command via V3 plugin system
 *
 * @param options Execution options
 * @returns Exit code (0 for success, non-zero for failure)
 *
 * @example
 * ```typescript
 * const exitCode = await executeCommandV3({
 *   pluginId: '@kb-labs/my-plugin',
 *   pluginVersion: '1.0.0',
 *   handlerPath: '/path/to/handler.js',
 *   argv: ['arg1', 'arg2'],
 *   flags: { verbose: true },
 *   ui: cliUIFacade,
 *   platform: platformServices,
 * });
 * ```
 */
export async function executeCommandV3(
  options: ExecuteCommandV3Options,
): Promise<number> {
  const {
    pluginId,
    pluginVersion,
    handlerPath,
    argv,
    flags,
    config: _config,
    permissions = {},
    cwd: _cwd = process.cwd(),
    outdir: _outdir,
    tenantId,
    ui,
    platform,
    signal,
    devMode: _devMode = false,
    socketPath: _socketPath,
    quotas,
    configSection,
  } = options;

  // Create plugin context descriptor
  const requestId = `cli-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
  const traceId = `trace-${randomUUID()}`;
  const spanId = `span-${randomUUID()}`;
  const invocationId = `inv-${randomUUID()}`;
  const executionId = `exec-${randomUUID()}`;

  const descriptor: PluginContextDescriptor = {
    hostType: "cli",
    pluginId,
    pluginVersion,
    tenantId,
    permissions,
    hostContext: { host: "cli", argv, flags },
    requestId,
    configSection,
  };
  Object.assign(descriptor as unknown as Record<string, unknown>, {
    traceId,
    spanId,
    invocationId,
    executionId,
  });

  // Prepare input
  const input = { argv, flags };

  try {
    // Resolve plugin root (required by ExecutionBackend). Prefer the
    // caller-supplied pkgRoot — the CLI discovery layer already knows
    // exactly where the plugin lives and this is the only path that
    // handles project-scope plugins whose deps live outside cwd.
    const pluginRoot =
      options.pluginRoot ?? resolvePluginRoot(pluginId, options.cwd ?? process.cwd());

    // Build ExecutionRequest for platform.executionBackend
    const request: ExecutionRequest = {
      executionId,
      descriptor,
      pluginRoot,
      handlerRef: handlerPath,
      input,
      env: options.env,
      timeoutMs: quotas?.timeoutMs,
      // Pass cwd so worker-pool backend sets ctx.cwd to the invocation directory,
      // not the platform installation directory (worker-script.ts reads workspace.cwd)
      workspace: { cwd: _cwd },
    };

    // Execute via unified platform.executionBackend
    // Backend respects platform.execution config (in-process, worker-pool, etc.)
    //
    // Use the platformContainer passed from v3-adapter (which got it from bootstrap)
    // This ensures we use the SAME instance that was initialized with ExecutionBackend
    const result =
      await options.platformContainer.executionBackend.execute(request, {
        signal,
      });

    // Handle execution result
    if (!result.ok) {
      presentExecutionError(platform, ui, result.error, requestId, Boolean(flags.json));
      return 1;
    }

    // Normalize the execution result for the CLI renderer.
    const executionMeta = result.metadata?.executionMeta ?? {
      startTime: Date.now(),
      endTime: Date.now(),
      duration: 0,
      pluginId,
      pluginVersion,
      requestId,
    };
    const runResult = {
      ok: true as const,
      data: result.data,
      executionMeta,
    };
    const cliResult = wrapCliResult(runResult, descriptor);

    if (flags.json) {
      if (cliResult.ok) {
        ui.json({ ok: true, data: cliResult.result ?? null, meta: cliResult.meta });
      } else {
        ui.json({ ok: false, error: toPluginErrorEnvelope(cliResult.error) });
      }
    } else if (!cliResult.ok) {
      const error = toPluginErrorEnvelope(cliResult.error);
      ui.error(`${error.message} [${error.code}]`, { hint: error.hint });
    }

    // Return exit code from wrapped result
    return cliResult.exitCode;
  } catch (error) {
    presentExecutionError(platform, ui, error, requestId, Boolean(flags.json));
    return 1;
  }
}

function presentExecutionError(
  platform: PlatformServices,
  ui: UIFacade,
  cause: unknown,
  requestId: string,
  jsonMode: boolean,
): void {
  const typed = extractExecutionError(cause);
  const correlationId = typed?.correlationId ?? randomUUID();
  const details = typed?.details && isStringMap(typed.details) ? typed.details : undefined;
  const code = typed?.code && getCatalogEntry(typed.code) ? typed.code : 'KB_RUNTIME_UNEXPECTED';
  const error = createErrorEnvelope(code, {
    correlationId,
    ...(code !== 'KB_RUNTIME_UNEXPECTED' && typed?.message ? { message: typed.message } : {}),
    ...(code !== 'KB_RUNTIME_UNEXPECTED' && details ? { details } : {}),
    ...(code !== 'KB_RUNTIME_UNEXPECTED' && typed?.hint ? { hint: typed.hint } : {}),
    ...(code === 'KB_RUNTIME_UNEXPECTED'
      ? { hint: `Run the command with --debug, then include correlation ID ${correlationId} in a report if it persists.` }
      : {}),
  });
  if (code === 'KB_RUNTIME_UNEXPECTED') {
    const loggedCause = cause instanceof Error ? cause : new Error(String(cause));
    platform.logger.error('Plugin command failed unexpectedly', loggedCause, {
      correlationId,
      requestId,
    });
  }
  if (jsonMode) {
    ui.json({ ok: false, error });
  } else {
    ui.error(`${error.message} [${error.code}]`, { hint: error.hint });
  }
}

function extractExecutionError(value: unknown): {
  code?: string;
  message?: string;
  hint?: string;
  correlationId?: string;
  details?: unknown;
} | undefined {
  if (value instanceof Error) {
    const error = value as Error & { code?: unknown; hint?: unknown; correlationId?: unknown; details?: unknown };
    return {
      code: typeof error.code === 'string' ? error.code : undefined,
      message: error.message,
      hint: typeof error.hint === 'string' ? error.hint : undefined,
      correlationId: typeof error.correlationId === 'string' ? error.correlationId : undefined,
      details: error.details,
    };
  }
  if (typeof value !== 'object' || value === null) return undefined;
  const error = 'error' in value ? value.error : value;
  if (typeof error !== 'object' || error === null) return undefined;
  const record = error as Record<string, unknown>;
  return {
    code: typeof record.code === 'string' ? record.code : undefined,
    message: typeof record.message === 'string' ? record.message : undefined,
    hint: typeof record.hint === 'string' ? record.hint : undefined,
    correlationId: typeof record.correlationId === 'string' ? record.correlationId : undefined,
    details: record.details,
  };
}

function toPluginErrorEnvelope(error: unknown) {
  if (
    typeof error === 'object' && error !== null &&
    'code' in error && typeof error.code === 'string' && getCatalogEntry(error.code) &&
    'message' in error && typeof error.message === 'string' &&
    'hint' in error && typeof error.hint === 'string'
  ) {
    const details = 'details' in error && isStringMap(error.details) ? error.details : undefined;
    return createErrorEnvelope(error.code, {
      message: error.message,
      hint: error.hint,
      correlationId: randomUUID(),
      ...(details ? { details } : {}),
    });
  }
  return createErrorEnvelope('KB_RUNTIME_UNEXPECTED', {
    message: 'Plugin command returned an invalid failure result.',
    hint: 'Update the plugin command to return a catalogued error code, message, and actionable hint.',
  });
}

function isStringMap(value: unknown): value is Record<string, string> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) &&
    Object.values(value).every((item) => typeof item === 'string');
}

/**
 * Fallback resolver for plugin root when the caller didn't supply one.
 *
 * Strategy: ask Node's resolver to find `<pluginId>/package.json` with a
 * search path rooted at `searchFrom`. This correctly handles hoisted
 * node_modules in pnpm workspaces and installed mode (the CLI wrapper
 * passes its cwd, which is the project root).
 *
 * This fallback is intentionally narrow — it can't find plugins whose
 * `node_modules` live outside the search path (e.g. a project-scope
 * plugin under `<projectRoot>/.kb/plugins/<name>/node_modules/` when the
 * CLI is invoked from elsewhere). For those callers must pass
 * `pluginRoot` explicitly.
 */
function resolvePluginRoot(pluginId: string, searchFrom: string): string {
  const nodeModulesPath = path.resolve(searchFrom, "node_modules", pluginId);
  try {
    const packageJson = require.resolve(`${pluginId}/package.json`, {
      paths: [searchFrom],
    });
    return path.dirname(packageJson);
  } catch {
    // Return the conventional path and let the execution backend surface
    // a clearer error (E_HANDLER_NOT_FOUND) than a generic resolve failure.
    return nodeModulesPath;
  }
}
