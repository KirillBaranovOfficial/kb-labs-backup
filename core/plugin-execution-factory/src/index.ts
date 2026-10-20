/**
 * @module @kb-labs/plugin-execution-factory
 *
 * Factory for creating execution backends - extracted to break circular dependencies.
 *
 * This package was extracted from @kb-labs/plugin-execution to eliminate the circular dependency:
 * core-runtime ↔ plugin-execution ↔ plugin-runtime
 *
 * Now the dependency chain is clean:
 * plugin-runtime → plugin-execution-factory → core-runtime
 *
 * Backend construction is intentionally available only to core runtime through
 * `@kb-labs/plugin-execution-factory/construct`.
 */

export { PROTOCOL_VERSION } from './types.js';
export type {
  StrictIsolationOptions,
  IsolatedBackendOptions,
  RemoteJobContext,
} from './isolated-backend.js';

// Worker-pool internals (re-exported for facade package compatibility)
export {
  DEFAULT_WORKER_POOL_CONFIG,
} from './backends/worker-pool/index.js';
export type {
  WorkerPoolConfig,
  WorkerState,
  WorkerInfo,
  WorkerPoolStats,
  QueuedRequest,
  WorkerMessage,
  ExecuteMessage,
  ResultMessage,
  ErrorMessage,
  HealthMessage,
  HealthOkMessage,
  ShutdownMessage,
  ReadyMessage,
} from './backends/worker-pool/index.js';

// Workspace
export {
  type WorkspaceManager,
  type WorkspaceLeaseContext,
  type WorkspaceLease,
  LocalWorkspaceManager,
  localWorkspaceManager,
} from './workspace/index.js';

// Errors and utils
export {
  ExecutionLayerError,
  TimeoutError,
  AbortError,
  HandlerContractError,
  HandlerNotFoundError,
  WorkspaceError,
  PermissionDeniedError,
  ValidationError,
  QueueFullError,
  AcquireTimeoutError,
  WorkerCrashedError,
  WorkerUnhealthyError,
  WorkerUnavailableError,
  isExecutionLayerError,
  isKnownErrorCode,
} from './errors.js';
export {
  createExecutionId,
  normalizeError,
  normalizeHeaders,
  createTimeoutPromise,
} from './utils.js';

// Types
export type {
  ExecutionBackend,
  BackendOptions,
  ResolvedExecutionMode,
  WorkerPoolOptions,
  WarmupPolicy,
  ExecutionRequest,
  ExecutionResponse,
  ExecutionResult,
  ExecutionError,
  ExecutionErrorCode,
  ExecutionMetadata,
  ExecuteOptions,
  LogEntry,
  OnLogCallback,
  HealthStatus,
  ExecutionStats,
  WorkspaceConfig,
  ArtifactsConfig,
  PluginHandler,
  HandlerMetadata,
  HandlerSchema,
  HostType,
  HostContext,
  PluginContextDescriptor,
  PermissionSpec,
  PluginInvokerFn,
  PlatformTransportFactory,
  PlatformTransportServer,
} from './types.js';
export type { NormalizedExecutionResult } from './normalized-result.js';

// Platform transport factories
export { IPCPlatformTransportFactory } from './platform-transport/index.js';
