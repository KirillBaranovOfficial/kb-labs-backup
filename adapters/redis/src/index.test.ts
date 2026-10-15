import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ILogger } from '@kb-labs/sdk/adapters';

const redisClient = vi.hoisted(() => ({
  listeners: new Map<string, (error: Error) => void>(),
  on: vi.fn((event: string, listener: (error: Error) => void) => { redisClient.listeners.set(event, listener); }),
  get: vi.fn(async () => null),
  quit: vi.fn(async () => 'OK'),
}));

vi.mock('ioredis', () => ({
  default: class {
    on = redisClient.on;
    get = redisClient.get;
    quit = redisClient.quit;
    constructor(..._args: unknown[]) {}
  },
}));

import { createAdapter } from './index.js';

describe('RedisCacheAdapter error visibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    redisClient.listeners.clear();
  });

  // case: S-211.2, S-211.4
  it('logs connection errors and returns a catalogued failure on the next command', async () => {
    const logger = {
      error: vi.fn(), info: vi.fn(), warn: vi.fn(), fatal: vi.fn(), debug: vi.fn(), trace: vi.fn(),
      child: vi.fn<(...args: Parameters<ILogger['child']>) => ILogger>(),
      flush: vi.fn().mockResolvedValue(undefined),
    } satisfies ILogger;
    const cache = createAdapter({}, { logger });
    const connectionError = new Error('connection lost');

    redisClient.listeners.get('error')?.(connectionError);

    expect(logger.error).toHaveBeenCalledWith('Redis cache connection error', connectionError, expect.objectContaining({
      adapter: 'redis',
      operation: 'connection',
    }));
    await expect(cache.get('key')).rejects.toMatchObject({
      code: 'KB_ADAPTER_OPERATION_FAILED',
      slot: 'cache',
      operation: 'get',
    });
  });
});
