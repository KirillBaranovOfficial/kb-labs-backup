import { describe, expect, it, vi } from 'vitest';
import type { WebSocket } from 'ws';
import { ConnectionRegistry } from '../connection-registry.js';

describe('WebSocket host operations', () => {
  it('routes send, broadcast, targeted send, and close through the host registry', async () => {
    const registry = new ConnectionRegistry();
    const current = { readyState: 1, send: vi.fn(), close: vi.fn() };
    const other = { readyState: 1, send: vi.fn(), close: vi.fn() };
    registry.register({ connectionId: 'current', channelPath: '/live', socket: current as unknown as WebSocket, metadata: {} });
    registry.register({ connectionId: 'other', channelPath: '/live', socket: other as unknown as WebSocket, metadata: {} });

    const first = { type: 'first', timestamp: 1 };
    const broadcast = { type: 'broadcast', timestamp: 2 };
    const targeted = { type: 'targeted', timestamp: 3 };

    await registry.hostOperations.send('current', first);
    await registry.hostOperations.broadcast('current', broadcast);
    await registry.hostOperations.broadcast('current', { type: 'self', timestamp: 3 }, false);
    await registry.hostOperations.sendTo('current', ['other'], targeted);
    await expect(registry.hostOperations.getConnectionId('current')).resolves.toBe('current');
    await registry.hostOperations.close('current', 4001, 'fixture complete');

    expect(current.send.mock.calls.map(([frame]) => JSON.parse(frame as string))).toEqual([first, { type: 'self', timestamp: 3 }]);
    expect(other.send.mock.calls.map(([frame]) => JSON.parse(frame as string))).toEqual([
      broadcast,
      { type: 'self', timestamp: 3 },
      targeted,
    ]);
    expect(current.close).toHaveBeenCalledWith(4001, 'fixture complete');
    expect(registry.getConnection('current')).toBeUndefined();
  });

  it.each(['unknown', 'closed'])('returns a catalogued error for a %s connection', async (state) => {
    const registry = new ConnectionRegistry();
    const socket = { readyState: state === 'closed' ? 3 : 1, send: vi.fn(), close: vi.fn() } as unknown as WebSocket;
    if (state === 'closed') {
      registry.register({ connectionId: 'connection', channelPath: '/live', socket, metadata: {} });
    }

    await expect(registry.hostOperations.send('connection', { type: 'late', timestamp: 1 })).rejects.toMatchObject({
      name: 'ErrorEnvelopeError',
      envelope: {
        code: 'KB_RUNTIME_WS_CONNECTION_UNAVAILABLE',
        hint: expect.any(String),
      },
    });
  });
});
