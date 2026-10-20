/**
 * @module @kb-labs/plugin-execution
 *
 * Universal execution layer for KB Labs plugins.
 *
 * ## v3 Unified Types
 *
 * This package uses types from @kb-labs/plugin-contracts directly:
 * - PluginContextDescriptor (not custom ExecutionDescriptor)
 * - HostContext (from plugin-contracts, not custom types)
 *
 * No type conversion needed - descriptor is passed to runInProcess() as-is.
 *
 * @example
 * ```typescript
 * import { mountRoutes } from '@kb-labs/plugin-execution/http';
 *
 * // Mount routes
 * await mountRoutes(server, manifest, { backend, pluginRoot, workspaceRoot });
 * ```
 */

// Types
export type {
  // Handler contract
  PluginHandler,
  HandlerMetadata,
  HandlerSchema,

  // Execution request/result
  ExecutionRequest,
  ExecutionResult,
  NormalizedExecutionResult,
  ExecutionError,
  ExecutionErrorCode,
  ExecutionMetadata,
  ExecuteOptions,
  LogEntry,
  OnLogCallback,
  PluginInvokerFn,

  // Runtime types (re-exported from plugin-contracts)
  // NOTE: No custom ExecutionDescriptor or HostContext - use runtime types directly!
  PluginContextDescriptor,
  HostContext,
  HostType,
  PermissionSpec,

  // Workspace
  WorkspaceConfig,
  ArtifactsConfig,

  // Backend interface
  ExecutionBackend,
  ResolvedExecutionMode,
  HealthStatus,
  ExecutionStats,

  // Backend options
  BackendOptions,
  WorkerPoolOptions,
  WarmupPolicy,
} from "./types.js";

export { PROTOCOL_VERSION } from "./types.js";

// Workspace
export {
  type WorkspaceManager,
  type WorkspaceLeaseContext,
  type WorkspaceLease,
  LocalWorkspaceManager,
  localWorkspaceManager,
} from "@kb-labs/plugin-execution-factory";

// Error classes (note: ExecutionLayerError, not ExecutionError - to avoid collision with interface)
export {
  // Phase 1: Core errors
  ExecutionLayerError,
  TimeoutError,
  AbortError,
  HandlerContractError,
  HandlerNotFoundError,
  WorkspaceError,
  PermissionDeniedError,
  ValidationError,
  // Phase 2: Pool-specific errors
  QueueFullError,
  AcquireTimeoutError,
  WorkerCrashedError,
  WorkerUnhealthyError,
  // Type guards
  isExecutionLayerError,
  isKnownErrorCode,
} from "./errors.js";

// Utils
export {
  createExecutionId,
  normalizeError,
  normalizeHeaders,
  createTimeoutPromise,
} from "./utils.js";

// WebSocket mounting
export {
  mountWebSocketChannels,
  type MountWebSocketChannelsOptions,
  connectionRegistry,
  ConnectionRegistry,
  type ConnectionInfo,
} from "./ws/index.js";
export {
  mountEventStreams,
  type MountEventStreamsOptions,
} from "./sse/index.js";
