/**
 * @module @kb-labs/plugin-execution/factory
 *
 * Factory for creating execution backends.
 * Simple by default, enterprise when needed.
 */

import type { BackendOptions, ExecutionBackend, ExecutionDiagnostics, ExecutionRequest, ExecutionResult, RawExecutionBackend } from './types.js';
import { InProcessBackend } from './backends/in-process.js';
import { SubprocessBackend } from './backends/subprocess.js';
import { WorkerPoolBackend } from './backends/worker-pool/backend.js';
import { RemoteBackend } from './backends/remote.js';
import { SubprocessRunnerAdapter } from './adapters/index.js';
import { BrokeredProcessExecutor, createDefaultProcessExecutor } from '@kb-labs/plugin-runtime';
import { isErrorEnvelope, type IPlatformAdapters } from '@kb-labs/core-platform';
import { isUxResultCandidate } from '@kb-labs/core-platform/ux-result';
import type { IResourceBroker } from '@kb-labs/core-resource-broker';
import type { ILogger } from '@kb-labs/core-platform';
import { normalizePluginHandlerFailure } from './plugin-handler-failure.js';
import { brandNormalizedExecutionResult, isFactoryNormalizedExecutionResult } from './normalized-result.js';
import type { NormalizedExecutionResult } from './normalized-result.js';
import { executionDiagnostics, normalizeExecutionFailure } from './utils.js';

export function ensureHostProcessExecutor(platform: BackendOptions['platform']): void {
  const host = platform as BackendOptions['platform'] & IPlatformAdapters & {
    hasResourceBroker?: boolean;
    resourceBroker?: IResourceBroker;
    setAdapter?: (key: string, value: unknown) => void;
  };
  if (host.processExecutor || !host.setAdapter || host.hasResourceBroker !== true || !host.resourceBroker) {
    return;
  }
  host.setAdapter('processExecutor', new BrokeredProcessExecutor(
    host.resourceBroker,
    createDefaultProcessExecutor(host.logger),
    host.logger,
  ));
}

/**
 * Create execution backend based on options.
 *
 * Mode detection:
 * - 'auto' (default): Detect based on environment
 * - 'in-process': Always use InProcessBackend (same process, no isolation)
 * - 'subprocess': Always use SubprocessBackend (single subprocess, process isolation)
 * - 'worker-pool': Always use WorkerPoolBackend (pool of workers, production-ready)
 * - 'remote': Always use RemoteExecutionBackend (remote executor service)
 *
 * @example
 * ```typescript
 * // Simplest - just works
 * const backend = createExecutionBackend({ platform });
 *
 * // Explicit mode
 * const backend = createExecutionBackend({
 *   platform,
 *   mode: 'worker-pool',
 *   workerPool: { min: 2, max: 10 },
 * });
 * ```
 */
export function createExecutionBackend(options: BackendOptions): ExecutionBackend {
  // Install the single host-owned executor before creating any backend. Worker
  // and subprocess platforms already expose a proxy and therefore are left
  // untouched; they forward execution to this host over IPC.
  ensureHostProcessExecutor(options.platform);
  const mode = options.mode === 'auto' || !options.mode
    ? detectMode()
    : options.mode;

  let backend: RawExecutionBackend;
  switch (mode) {
    case 'in-process':
      backend = new InProcessBackend({
        platform: options.platform,
        uiProvider: options.uiProvider,
        pluginInvoker: options.pluginInvoker,
      });
      break;

    case 'subprocess':
      backend = new SubprocessBackend({
        platform: options.platform,
        runner: new SubprocessRunnerAdapter(),
        uiProvider: options.uiProvider,
      });
      break;

    // eslint-disable-next-line sonarjs/no-duplicate-string
    case 'worker-pool':
      backend = new WorkerPoolBackend({
        platform: options.platform,
        platformTransport: options.platformTransport,
        uiProvider: options.uiProvider,
        min: options.workerPool?.min ?? 2,
        max: options.workerPool?.max ?? 10,
        maxRequestsPerWorker: options.workerPool?.maxRequestsPerWorker ?? 1000,
        maxUptimeMsPerWorker: options.workerPool?.maxUptimeMsPerWorker ?? 30 * 60 * 1000,
        maxConcurrentPerPlugin: options.workerPool?.maxConcurrentPerPlugin,
        warmup: options.workerPool?.warmup ?? { mode: 'none', topN: 5, maxHandlers: 20 },
      });
      break;

    case 'remote': {
      if (!options.remote?.transport) {
        throw new Error('Remote mode requires options.remote.transport (IExecutionTransport)');
      }
      const remoteBackend = new RemoteBackend({
        transport: options.remote.transport,
        workspaceRootOnHost: options.remote.workspaceRootOnHost,
      });
      backend = createRoutingBackend(options, remoteBackend);
      break;
    }

    default:
      throw new Error(`Unknown execution mode: ${mode}`);
  }
  return createNormalizedExecutionBackend(backend, options.platform.logger);
}

/** Every execution backend crosses this owner before a result reaches a surface. */
export function createNormalizedExecutionBackend(
  backend: DispatchExecutionBackend,
  logger: ILogger,
): ExecutionBackend {
  return new NormalizingExecutionBackend(backend, logger);
}

class NormalizingExecutionBackend implements ExecutionBackend {
  constructor(private readonly backend: DispatchExecutionBackend, private readonly logger: ILogger) {}

  async execute(request: ExecutionRequest, options?: Parameters<ExecutionBackend['execute']>[1]): Promise<NormalizedExecutionResult> {
    let result: ExecutionResult;
    try {
      result = await this.backend.execute(request, options);
    } catch (error) {
      const failure = normalizeExecutionFailure(error, request);
      result = {
        ok: false as const,
        error: failure.error,
        diagnostics: failure.diagnostics,
        executionTimeMs: 0,
      };
    }
    if (isFactoryNormalizedExecutionResult(result)) return result;

    let returnedFailure = false;
    let returnedFailureError: unknown;
    if (result.ok) {
      if (isUxResultCandidate(result.data) && result.data.ok === false) {
        returnedFailure = true;
        returnedFailureError = result.data.error;
        const failure = normalizeExecutionFailure(result.data.error, request);
        result = {
          ...result,
          ok: false,
          diagnostics: failure.diagnostics,
          error: failure.error,
        };
      } else {
        return brandNormalizedExecutionResult({ ...result, ok: true as const });
      }
    }

    let executionError = result.error;
    let executionDiagnostics = result.diagnostics;
    if (!executionError) {
      const missingFailure = normalizeExecutionFailure(new Error('Backend returned a failed execution without an error value.'), request);
      executionError = missingFailure.error;
      executionDiagnostics ??= missingFailure.diagnostics;
    }

    const descriptor = request.descriptor;
    const correlationId = descriptor.requestId || request.executionId;
    const hostType = descriptor.hostType;
    const { diagnostics: _diagnostics, ...normalizedResult } = result;
    const { error: _serializedError, ...executionEnvelope } = executionError;
    const error = normalizePluginHandlerFailure(executionError, {
      pluginId: descriptor.pluginId,
      pluginVersion: descriptor.pluginVersion,
      command: request.context?.commandPath ?? request.handlerRef,
      correlationId,
      logger: this.logger,
      diagnosticStack: executionDiagnostics?.errorStack,
      serializedError: executionError.error,
      returnedFailureError,
      executionEnvelope,
      logContext: {
        applicationId: 'kb-labs',
        serviceId: `${hostType}-plugin-execution`,
        instanceId: String(process.pid),
        layer: hostType,
      },
      returnedFailure,
    });
    return brandNormalizedExecutionResult({
      ...normalizedResult,
      ok: false,
      error: {
        ...error,
        error: executionError.error,
        envelope: error,
      },
    });
  }

  health() { return this.backend.health(); }
  stats() { return this.backend.stats(); }
  shutdown() { return this.backend.shutdown(); }
}


interface DispatchExecutionBackend {
  execute(request: ExecutionRequest, options?: Parameters<ExecutionBackend['execute']>[1]): Promise<import('./types.js').ExecutionResult | NormalizedExecutionResult>;
  health(): ReturnType<RawExecutionBackend['health']>;
  stats(): ReturnType<RawExecutionBackend['stats']>;
  shutdown(): ReturnType<RawExecutionBackend['shutdown']>;
}

/**
 * Detect execution mode based on environment (may include 'remote').
 */
function detectMode(): BackendOptions['mode'] {
  // Remote if executor endpoint is configured
  if (process.env.EXECUTOR_SERVICE_ENDPOINT) {
    return 'remote';
  }

  // Worker pool if explicitly enabled or Kubernetes
  if (
    process.env.EXECUTION_MODE === 'worker-pool' ||
    process.env.KUBERNETES_SERVICE_HOST
  ) {
    return 'worker-pool';
  }

  // Default: in-process (simplest)
  return 'in-process';
}

/**
 * Detect local execution mode — never returns 'remote'.
 */
function detectLocalMode(): 'in-process' | 'subprocess' | 'worker-pool' {
  if (
    process.env.EXECUTION_MODE === 'worker-pool' ||
    process.env.KUBERNETES_SERVICE_HOST
  ) {
    return 'worker-pool';
  }
  if (process.env.EXECUTION_MODE === 'subprocess') {
    return 'subprocess';
  }
  return 'in-process';
}

/**
 * Wrap a local backend with routing logic:
 * if request.target.environmentId is set → RemoteBackend,
 * otherwise → local backend.
 *
 * This allows isolation: strict to coexist with isolation: relaxed/balanced
 * without changing call sites — the backend decides dynamically.
 */
function createRoutingBackend(options: BackendOptions, remoteBackend: RemoteBackend): RawExecutionBackend {
  const localBackend = createLocalBackend(options);

  return {
    async execute(request: ExecutionRequest, execOptions?) {
      if (request.target?.environmentId) {
        return remoteBackend.execute(request, execOptions);
      }
      return localBackend.execute(request, execOptions);
    },
    health: () => localBackend.health(),
    stats: () => localBackend.stats(),
    shutdown: async () => {
      await Promise.all([localBackend.shutdown(), remoteBackend.shutdown()]);
    },
  };
}

/**
 * Create a local (non-remote) backend based on options.
 * Used internally by createRoutingBackend.
 * Never returns remote — remote routing is handled by createRoutingBackend.
 */
function createLocalBackend(options: BackendOptions): RawExecutionBackend {
  const mode = detectLocalMode();
  switch (mode) {
    case 'in-process':
      return new InProcessBackend({
        platform: options.platform,
        uiProvider: options.uiProvider,
        pluginInvoker: options.pluginInvoker,
      });
    case 'subprocess':
      return new SubprocessBackend({
        platform: options.platform,
        runner: new SubprocessRunnerAdapter(),
        uiProvider: options.uiProvider,
      });
    case 'worker-pool':
      return new WorkerPoolBackend({
        platform: options.platform,
        platformTransport: options.platformTransport,
        uiProvider: options.uiProvider,
        min: options.workerPool?.min ?? 2,
        max: options.workerPool?.max ?? 10,
        maxRequestsPerWorker: options.workerPool?.maxRequestsPerWorker ?? 1000,
        maxUptimeMsPerWorker: options.workerPool?.maxUptimeMsPerWorker ?? 30 * 60 * 1000,
        maxConcurrentPerPlugin: options.workerPool?.maxConcurrentPerPlugin,
        warmup: options.workerPool?.warmup ?? { mode: 'none', topN: 5, maxHandlers: 20 },
      });
    default:
      return new InProcessBackend({
        platform: options.platform,
        uiProvider: options.uiProvider,
        pluginInvoker: options.pluginInvoker,
      });
  }
}
