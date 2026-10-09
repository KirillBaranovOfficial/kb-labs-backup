import { ErrorEnvelopeError } from '@kb-labs/core-platform';
import { describe, expect, it } from 'vitest';
import { requireProjectContext } from '../project-context.js';

describe('requireProjectContext', () => {
  it('returns a registered project root', () => {
    expect(requireProjectContext('/workspace/project', '/outside')).toBe('/workspace/project');
  });

  it('raises the catalogued project error when the root is absent', () => {
    let caught: unknown;
    try {
      requireProjectContext(undefined, '/workspace/unregistered');
    } catch (error) {
      caught = error;
    }

    expect(caught).toBeInstanceOf(ErrorEnvelopeError);
    if (caught instanceof ErrorEnvelopeError) {
      expect(caught.envelope).toMatchObject({
        code: 'KB_PROJECT_UNKNOWN',
        message: 'The project /workspace/unregistered is not in the registry (the folder may have moved).',
        hint: 'Register it with "kb project add /workspace/unregistered".',
      });
    }
  });
});
