/**
 * @module @kb-labs/core-runtime/proxy/create-proxy-platform
 * Create platform container with proxy adapters for host-managed service calls.
 *
 * This function creates a PlatformContainer where all adapters are proxies
 * that forward calls through the supplied host-owned transport.
 *
 * Used by execution hosts to route platform requests to their configured service transport.
 */

import type { PlatformContainer } from '../container.js';
import type { ITransport } from '../transport/transport.js';
import { CacheProxy } from './cache-proxy.js';
import { LLMProxy } from './llm-proxy.js';
import { EmbeddingsProxy } from './embeddings-proxy.js';
import { VectorStoreProxy } from './vector-store-proxy.js';
import { StorageProxy } from './storage-proxy.js';
import { DocumentDatabaseProxy } from './document-database-proxy.js';
import { KVStoreProxy } from './kv-store-proxy.js';
import type { ILogger, IEventBus, IAnalytics } from '@kb-labs/core-platform';
import { ProcessExecutorProxy } from './process-executor-proxy.js';

export interface CreateProxyPlatformOptions {
  /** Required host-owned transport for platform RPC. */
  transport: ITransport;

  /**
   * Logger for subprocess (can be noop or local file logger).
   * Note: This logger is NOT proxied - logs are local to subprocess.
   */
  logger?: ILogger;
}

/**
 * Noop logger for subprocess (doesn't proxy to parent).
 *
 * Logging in subprocess should be either:
 * 1. Local (to subprocess logs)
 * 2. Sent via events to parent
 * 3. Noop (discarded)
 *
 * Never proxy logger calls via IPC - too chatty!
 */
function createNoopLogger(): ILogger {
  const noop = () => {};
  const logger: ILogger = {
    debug: noop,
    info: noop,
    warn: noop,
    error: noop,
    fatal: noop,
    trace: noop,
    flush: async () => {},
    child: () => logger,
  };
  return logger;
}

/**
 * Create platform container with proxy adapters.
 *
 * All adapters (except logger) forward calls over the supplied host-owned transport.
 * This allows subprocess handlers to access real platform services.
 *
 * @param options - Configuration options
 * @returns Platform container with proxy adapters
 *
 * @example
 * ```typescript
 * // In subprocess (worker-script.ts)
 * import { createProxyPlatform } from '@kb-labs/core-runtime/proxy';
 *
 * const platform = await createProxyPlatform({
 *   transport,
 *   logger: createFileLogger('/tmp/worker.log'),
 * });
 *
 * // Now handlers can use real platform services
 * await platform.cache.set('key', 'value');
 * const result = await platform.llm.complete({ prompt: 'Hello' });
 * ```
 */
export async function createProxyPlatform(
  options: CreateProxyPlatformOptions
): Promise<PlatformContainer> {
  const { transport } = options;

  // Create proxy adapters
  const cache = new CacheProxy(transport);
  const llm = new LLMProxy(transport);
  const embeddings = new EmbeddingsProxy(transport);
  const vectorStore = new VectorStoreProxy(transport);
  const storage = new StorageProxy(transport);
  const documentDatabase = new DocumentDatabaseProxy(transport);
  const kvStore = new KVStoreProxy(transport);

  // Logger is NOT proxied (local or noop)
  const logger = options.logger ?? createNoopLogger();

  // EventBus - TODO: implement EventBusProxy
  // For now, noop (job handlers use ctx.events, not platform.eventBus)
  const eventBus = {
    on: () => () => {},
    once: () => () => {},
    off: () => {},
    emit: async () => {},
  };

  // Analytics - TODO: implement AnalyticsProxy
  // For now, noop (low priority)
  const analytics = {
    track: () => {},
    identify: () => {},
    flush: async () => {},
  };

  // Create new PlatformContainer and configure adapters
  const platform = new (await import('../container.js')).PlatformContainer();
  platform.setAdapter('logger', logger);
  platform.setAdapter('cache', cache);
  platform.setAdapter('llm', llm);
  platform.setAdapter('embeddings', embeddings);
  platform.setAdapter('vectorStore', vectorStore);
  platform.setAdapter('storage', storage);
  platform.setAdapter('documentDatabase', documentDatabase);
  platform.setAdapter('kvStore', kvStore);
  platform.setAdapter('eventBus', eventBus as unknown as IEventBus);
  platform.setAdapter('analytics', analytics as unknown as IAnalytics);
  platform.setAdapter('processExecutor', new ProcessExecutorProxy(transport));

  return platform;
}

/**
 * Close proxy platform and disconnect from parent.
 *
 * Should be called when subprocess is shutting down.
 *
 * @param platform - Platform container created by createProxyPlatform()
 *
 * @example
 * ```typescript
 * const platform = await createProxyPlatform({ transport: gatewayTransport });
 * // ... use platform ...
 * await closeProxyPlatform(platform);
 * ```
 */
export async function closeProxyPlatform(platform: PlatformContainer): Promise<void> {
  // Close transport if it has a close method
  // @ts-expect-error - accessing internal transport
  const transport = platform.cache?._transport;
  if (transport && typeof transport.close === 'function') {
    await transport.close();
  }
}
