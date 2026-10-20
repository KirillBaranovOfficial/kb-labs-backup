import { describe, expect, it } from 'vitest';
import type { ExecuteMessage } from '../sandbox/ipc-protocol.js';

describe('stdio execution payload', () => {
  it('contains execution data without a socket path or carrier-specific fields', () => {
    const payload: ExecuteMessage = {
      requestId: 'exec-1',
      descriptor: {
        hostType: 'cli',
        pluginId: '@kb-labs/test',
        pluginVersion: '1.0.0',
        requestId: 'request-1',
        permissions: {},
        hostContext: { host: 'cli', binaryName: 'kb', argv: [], flags: {} },
      },
      handlerPath: '/workspace/handler.mjs',
      input: { action: 'run' },
      cwd: '/workspace',
      strictFilesystemPermissions: false,
    };

    expect(payload).toMatchObject({ requestId: 'exec-1', handlerPath: '/workspace/handler.mjs' });
    expect(payload).not.toHaveProperty('socketPath');
  });
});
