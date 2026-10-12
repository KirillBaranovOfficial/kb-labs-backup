import type { ICache } from "@kb-labs/sdk/adapters";
import { getHostServiceAddress, type HostServiceEnvironment } from "@kb-labs/sdk";
import { createBroker, type Broker } from "./broker.js";

export { manifest } from "./manifest.js";

export interface StateBrokerCacheConfig {
  namespace?: string;
  /** Runtime-only environment supplied by the host when assembling its own platform. */
  hostServiceEnvironment?: HostServiceEnvironment;
}

export class StateBrokerCacheAdapter implements ICache {
  private brokerInstance?: Broker;
  private readonly namespace: string;
  private readonly hostServiceEnvironment?: HostServiceEnvironment;

  constructor(config: StateBrokerCacheConfig = {}) {
    this.namespace = config.namespace ?? "kb:";
    this.hostServiceEnvironment = config.hostServiceEnvironment;
  }

  async get<T>(key: string): Promise<T | null> {
    return this.broker.get<T>(this.key(key));
  }

  async set<T>(key: string, value: T, ttl?: number): Promise<void> {
    return this.broker.set(this.key(key), value, ttl);
  }

  async delete(key: string): Promise<void> {
    return this.broker.delete(this.key(key));
  }

  async clear(pattern?: string): Promise<void> {
    return this.broker.clear(this.key(pattern ?? "*"));
  }

  async zadd(key: string, score: number, member: string): Promise<void> {
    return this.broker.zadd(this.key(key), score, member);
  }

  async zrangebyscore(key: string, min: number, max: number): Promise<string[]> {
    return this.broker.zrangebyscore(this.key(key), min, max);
  }

  async zrem(key: string, member: string): Promise<void> {
    return this.broker.zrem(this.key(key), member);
  }

  async setIfNotExists<T>(key: string, value: T, ttl?: number): Promise<boolean> {
    return this.broker.setIfNotExists(this.key(key), value, ttl);
  }

  private key(key: string): string {
    return `${this.namespace}${key}`;
  }

  private get broker(): Broker {
    this.brokerInstance ??= createBroker({
      url: getHostServiceAddress("state-broker", this.hostServiceEnvironment),
    });
    return this.brokerInstance;
  }
}

export function createAdapter(
  config?: StateBrokerCacheConfig,
): StateBrokerCacheAdapter {
  return new StateBrokerCacheAdapter(config);
}

export default createAdapter;
