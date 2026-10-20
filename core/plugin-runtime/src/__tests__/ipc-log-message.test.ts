import { describe, expect, it } from 'vitest';
import { StdioFrameDecoder, encodeStdioFrame } from '@kb-labs/core-ipc';

describe('stdio log frame', () => {
  it('uses the shared protocol frame for child stdout logs', () => {
    const encoded = encodeStdioFrame({
      type: 'log',
      level: 'info',
      message: 'Building project...',
      stream: 'stdout',
      meta: { requestId: 'req-1' },
    });
    expect(new StdioFrameDecoder().push(encoded)).toEqual([{
      type: 'log',
      level: 'info',
      message: 'Building project...',
      stream: 'stdout',
      meta: { requestId: 'req-1' },
    }]);
  });
});
