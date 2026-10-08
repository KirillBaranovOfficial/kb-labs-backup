import { describe, expect, it } from 'vitest';
import { createErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';
import { rebuildCataloguedError, serializeCataloguedError } from './error-codec.js';

describe('worker IPC catalogued error codec', () => {
  it('round-trips the complete catalogued error context', () => {
    const original = new ErrorEnvelopeError(createErrorEnvelope('KB_RUNTIME_INPUT_INVALID', {
      message: 'Prompt input is required.',
      hint: 'Supply the value as command input and retry.',
      cause: 'The prompt "workspace" has no responder.',
      details: { field: 'workspace', source: 'prompt' },
    }));

    const decoded = rebuildCataloguedError(serializeCataloguedError(original));

    expect(decoded.envelope).toMatchObject({
      code: 'KB_RUNTIME_INPUT_INVALID',
      message: 'Prompt input is required.',
      hint: 'Supply the value as command input and retry.',
      cause: 'The prompt "workspace" has no responder.',
      details: { field: 'workspace', source: 'prompt' },
    });
  });
});
