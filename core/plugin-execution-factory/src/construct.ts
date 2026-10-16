/** Backend construction is reserved for the platform runtime. */
export { createExecutionBackend, createNormalizedExecutionBackend, ensureHostProcessExecutor } from './factory.js';
export { createIsolatedExecutionBackend } from './isolated-backend.js';
export type {
  StrictIsolationOptions,
  IsolatedBackendOptions,
  RemoteJobContext,
} from './isolated-backend.js';
