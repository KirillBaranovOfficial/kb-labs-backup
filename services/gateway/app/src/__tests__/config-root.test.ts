import { describe, expect, it } from 'vitest';
import { ErrorEnvelopeError } from '@kb-labs/core-platform';
import { resolveGatewayConfigRoot } from '../bootstrap.js';

describe('resolveGatewayConfigRoot', () => {
  it('selects the platform root for embedded platform scope', () => {
    expect(resolveGatewayConfigRoot({ platformRoot: '/platform', projectRoot: '/project' }, 'platform'))
      .toBe('/platform');
  });

  it('returns the registered project root for project scope', () => {
    expect(resolveGatewayConfigRoot({ platformRoot: '/platform', projectRoot: '/project' }, 'project'))
      .toBe('/project');
  });

  it('uses the shared catalogued error when project scope has no root', () => {
    expect(() => resolveGatewayConfigRoot({ platformRoot: '/platform' }, 'project'))
      .toThrowError(ErrorEnvelopeError);
    try {
      resolveGatewayConfigRoot({ platformRoot: '/platform' }, 'project');
    } catch (error) {
      expect((error as ErrorEnvelopeError).envelope.code).toBe('KB_PROJECT_UNKNOWN');
      return;
    }
    throw new Error('Expected missing project context to fail');
  });
});
