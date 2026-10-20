/**
 * @module @kb-labs/core-runtime/transport
 * Transport layer for cross-process adapter communication.
 *
 * This module provides the transport abstraction used by platform proxy
 * adapters. The execution control channel is owned by @kb-labs/core-ipc.
 *
 * @example
 * ```typescript
 * import type { ITransport } from '@kb-labs/core-runtime/transport';
 * const response = await transport.send(call);
 * await transport.close();
 * ```
 */

export * from './transport.js';
export * from './timeout-config.js';
