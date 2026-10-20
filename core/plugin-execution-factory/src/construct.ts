/** Backend construction is reserved for the platform runtime. */
export { createExecutionBackend, createNormalizedExecutionBackend, ensureHostProcessExecutor } from './factory.js';
export { createIsolatedExecutionBackend } from './isolated-backend.js';
export { containerUnavailableError, requireAttachedEnvironmentProvider, ContainerProviderUnavailableError } from './isolated-backend.js';
export { StdioExecutionTransport } from './backends/stdio-execution-transport.js';
export {
  assertContainerNetworkPermissionsSupported,
  createContainerExecutionSpec,
} from './container-spec.js';
export type { ContainerExecutionSpec, ContainerExecutionInputs } from './container-spec.js';
export type {
  StrictIsolationOptions,
  IsolatedBackendOptions,
  RemoteJobContext,
} from './isolated-backend.js';
