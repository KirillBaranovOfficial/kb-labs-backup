/**
 * @module @kb-labs/plugin-execution/backends
 */

export { InProcessBackend, type InProcessBackendOptions } from './in-process.js';
export { SubprocessBackend, type SubprocessBackendOptions } from './subprocess.js';
export { WorkerPoolBackend } from './worker-pool/backend.js';
export type { WorkerPoolBackendOptions } from './worker-pool/types.js';
