/**
 * @module @kb-labs/adapters-redis
 * Redis adapter implementing ICache interface.
 *
 * @example
 * ```typescript
 * import { createAdapter } from '@kb-labs/adapters-redis';
 *
 * const cache = createAdapter({
 *   host: 'localhost',
 *   port: 6379,
 * });
 *
 * await cache.set('user:123', { name: 'Alice' }, 60000); // TTL 60s
 * const user = await cache.get('user:123');
 * await cache.delete('user:123');
 * await cache.clear('user:*');
 * ```
 */

import Redis, { type RedisOptions } from "ioredis";
import type { ICache } from "@kb-labs/sdk/adapters";
import type { ILogger } from "@kb-labs/sdk/adapters";
import { AdapterOperationError } from "@kb-labs/sdk";

// Re-export manifest
export { manifest } from "./manifest.js";

/**
 * Configuration for Redis cache adapter.
 */
export interface RedisCacheConfig extends RedisOptions {
  /** Redis host (default: 'localhost') */
  host?: string;
  /** Redis port (default: 6379) */
  port?: number;
  /** Redis connection URL — takes precedence over host/port (e.g. 'redis://kb-redis:6379') */
  url?: string;
  /** Key prefix for all cache keys (default: 'kb:') */
  keyPrefix?: string;
}

/**
 * Redis implementation of ICache interface.
 */
export class RedisCacheAdapter implements ICache {
  private client: Redis;
  private keyPrefix: string;
  private pendingConnectionError?: Error;
  private readonly logger?: ILogger;

  constructor(config: RedisCacheConfig = {}, logger?: ILogger) {
    const { url, keyPrefix, ...redisOptions } = config;
    this.keyPrefix = keyPrefix ?? "kb:";
    this.logger = logger;

    if (url) {
      this.client = new Redis(url, {
        lazyConnect: true,
        ...redisOptions,
        keyPrefix: this.keyPrefix,
      });
    } else {
      this.client = new Redis({
        lazyConnect: true,
        host: redisOptions.host ?? "localhost",
        port: redisOptions.port ?? 6379,
        ...redisOptions,
        keyPrefix: this.keyPrefix,
      });
    }

    this.client.on("error", (error) => {
      this.pendingConnectionError = error;
      this.logger?.error("Redis cache connection error", error, {
        adapter: "redis",
        operation: "connection",
        endpoint: url ? "configured-url" : `${redisOptions.host ?? "localhost"}:${redisOptions.port ?? 6379}`,
      });
    });
  }

  private async run<T>(operation: string, action: () => Promise<T>): Promise<T> {
    if (this.pendingConnectionError) {
      const error = this.pendingConnectionError;
      this.pendingConnectionError = undefined;
      throw new AdapterOperationError("cache", operation, "Redis reported a connection failure before this cache operation.", error);
    }
    try {
      return await action();
    } catch (error) {
      throw new AdapterOperationError("cache", operation, "Redis cache operation failed.", error);
    }
  }

  async get<T>(key: string): Promise<T | null> {
    const value = await this.run("get", () => this.client.get(key));
    if (value === null) {
      return null;
    }

    try {
      return JSON.parse(value) as T;
    } catch {
      // If not JSON, return as-is (cast to T)
      return value as T;
    }
  }

  async set<T>(key: string, value: T, ttl?: number): Promise<void> {
    const serialized = JSON.stringify(value);

    if (ttl !== undefined) {
      // TTL in milliseconds, Redis uses seconds
      await this.run("set", () => this.client.setex(key, Math.ceil(ttl / 1000), serialized));
    } else {
      await this.run("set", () => this.client.set(key, serialized));
    }
  }

  async delete(key: string): Promise<void> {
    await this.run("delete", () => this.client.del(key));
  }

  async clear(pattern?: string): Promise<void> {
    if (!pattern) {
      // Clear all keys with our prefix
      await this.run("clear", () => this.client.flushdb());
      return;
    }

    // Find keys matching pattern
    const fullPattern = `${this.keyPrefix}${pattern}`;
    const keys = await this.run("clear", () => this.client.keys(fullPattern));

    if (keys.length > 0) {
      // Remove prefix before deleting (ioredis adds it automatically)
      const keysWithoutPrefix = keys.map((k) => k.slice(this.keyPrefix.length));
      await this.run("clear", () => this.client.del(...keysWithoutPrefix));
    }
  }

  // ═══════════════════════════════════════════════════════════════════════
  // Sorted Set Operations
  // ═══════════════════════════════════════════════════════════════════════

  async zadd(key: string, score: number, member: string): Promise<void> {
    await this.run("zadd", () => this.client.zadd(key, score, member));
  }

  async zrangebyscore(
    key: string,
    min: number,
    max: number,
  ): Promise<string[]> {
    return this.run("zrangebyscore", () => this.client.zrangebyscore(key, min, max));
  }

  async zrem(key: string, member: string): Promise<void> {
    await this.run("zrem", () => this.client.zrem(key, member));
  }

  // ═══════════════════════════════════════════════════════════════════════
  // Atomic Operations
  // ═══════════════════════════════════════════════════════════════════════

  async setIfNotExists<T>(
    key: string,
    value: T,
    ttl?: number,
  ): Promise<boolean> {
    const serialized = JSON.stringify(value);

    if (ttl !== undefined) {
      // SET key value PX milliseconds NX
      const result = await this.run("setIfNotExists", () => this.client.set(key, serialized, "PX", ttl, "NX"));
      return result === "OK";
    } else {
      // SET key value NX (no TTL)
      const result = await this.run("setIfNotExists", () => this.client.set(key, serialized, "NX"));
      return result === "OK";
    }
  }

  /**
   * Close Redis connection.
   * Call this on app shutdown.
   */
  async disconnect(): Promise<void> {
    await this.run("disconnect", () => this.client.quit());
  }
}

/**
 * Create Redis cache adapter.
 * This is the factory function called by initPlatform() when loading adapters.
 */
export function createAdapter(config?: RedisCacheConfig, deps: { logger?: ILogger } = {}): RedisCacheAdapter {
  return new RedisCacheAdapter(config, deps.logger);
}

// Default export for direct import
export default createAdapter;
// test
