/**
 * @module @kb-labs/plugin-execution/factory
 *
 * Factory for creating execution backends.
 * Simple by default, enterprise when needed.
 */

import type { BackendOptions, ExecutionBackend, ExecutionRequest } from './types.js';
import { InProcessBackend } from './backends/in-process.js';
import { SubprocessBackend } from './backends/subprocess.js';
import { WorkerPoolBackend } from './backends/worker-pool/backend.js';
import { RemoteBackend } from './backends/remote.js';
import { SubprocessRunnerAdapter } from './adapters/index.js';
import { BrokeredProcessExecutor, createDefaultProcessExecutor } from '@kb-labs/plugin-runtime';
import type { IPlatformAdapters } from '@kb-labs/core-platform';
import type { IResourceBroker } from '@kb-labs/core-resource-broker';
import { normalizePluginHandlerFailure, type ILogger } from '@kb-labs/core-platform';

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

  let backend: ExecutionBackend;
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
  return new NormalizingExecutionBackend(backend, options.platform.logger);
}

/** Every execution backend crosses this owner before a result reaches a surface. */
class NormalizingExecutionBackend implements ExecutionBackend {
  constructor(private readonly backend: ExecutionBackend, private readonly logger: ILogger) {}

  async execute(request: ExecutionRequest, options?: Parameters<ExecutionBackend['execute']>[1]) {
    let result;
    try {
      result = await this.backend.execute(request, options);
    } catch (error) {
      result = {
        ok: false as const,
        error,
        executionTimeMs: 0,
      };
    }
    if (result.ok) return result;

    const descriptor = request.descriptor;
    const correlationId = descriptor.requestId || request.executionId;
    const hostType = descriptor.hostType;
    const error = normalizePluginHandlerFailure(result.error, {
      pluginId: descriptor.pluginId,
      command: request.context?.commandPath ?? request.handlerRef,
      correlationId,
      logger: this.logger,
      logContext: {
        applicationId: 'kb-labs',
        serviceId: `${hostType}-plugin-execution`,
        instanceId: String(process.pid),
        layer: hostType,
      },
      sensitiveValues: Object.entries(request.env ?? {})
        .filter(([key]) => /(?:secret|token|password|credential|api[_-]?key)/i.test(key))
        .map(([, value]) => value)
        .filter((value): value is string => value !== undefined),
    });
    return { ...result, ok: false as const, error: { ...error } };
  }

  health() { return this.backend.health(); }
  stats() { return this.backend.stats(); }
  shutdown() { return this.backend.shutdown(); }
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
function createRoutingBackend(options: BackendOptions, remoteBackend: RemoteBackend): ExecutionBackend {
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
function createLocalBackend(options: BackendOptions): ExecutionBackend {
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
