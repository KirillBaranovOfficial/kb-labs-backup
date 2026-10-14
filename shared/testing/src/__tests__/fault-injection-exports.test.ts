import { describe, expect, it } from 'vitest';
import * as platformInMemory from '@kb-labs/core-platform/inmemory';
import * as sharedTesting from '../index.js';

describe('fault-injection adapter ownership', () => {
  it('keeps failing adapters in shared testing, outside the production in-memory API', () => {
    expect(sharedTesting).toHaveProperty('FaultInjectingCache');
    expect(sharedTesting).toHaveProperty('FaultInjectingEventBus');
    expect(sharedTesting).toHaveProperty('FaultInjectingStorage');
    expect(platformInMemory).not.toHaveProperty('FaultInjectingCache');
    expect(platformInMemory).not.toHaveProperty('FaultInjectingEventBus');
  });
});
