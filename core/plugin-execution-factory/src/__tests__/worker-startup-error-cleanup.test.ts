import { EventEmitter } from 'node:events';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ChildProcess } from 'node:child_process';
import type { PlatformTransportFactory } from '../types.js';

const { forkMock } = vi.hoisted(() => ({ forkMock: vi.fn() }));
vi.mock('node:child_process', async (importOriginal) => ({
  ...await importOriginal<typeof import('node:child_process')>(),
  fork: forkMock,
}));

const { Worker } = await import('../backends/worker-pool/worker.js');

describe('Worker startup error cleanup', () => {
  afterEach(() => { vi.clearAllMocks(); });

  it('closes the transport server when the child process errors during startup', async () => {
    const child = Object.assign(new EventEmitter(), {
      send: vi.fn(),
      kill: vi.fn(),
      pid: undefined,
    }) as unknown as ChildProcess;
    forkMock.mockReturnValue(child);

    const stop = vi.fn();
    const transport: PlatformTransportFactory = {
      type: 'test',
      createServer: () => ({ start: vi.fn(), stop }),
    };
    const worker = new Worker({
      workerScript: '/unused/worker.mjs',
      platform: {} as never,
      platformTransport: transport,
      startupTimeoutMs: 1_000,
    });

    const startup = worker.spawn();
    child.emit('error', new Error('spawn failed'));

    await expect(startup).rejects.toThrow('spawn failed');
    expect(stop).toHaveBeenCalledOnce();
    worker.kill();
  });
});
