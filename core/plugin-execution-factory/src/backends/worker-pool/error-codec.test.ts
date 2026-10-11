import { describe, expect, it } from 'vitest';
import { createErrorEnvelope, createProductErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';
import { rebuildCataloguedError, serializeCataloguedError } from './error-codec.js';

describe('worker IPC error codec', () => {
  it('round-trips the complete platform error context', () => {
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

  it('round-trips a product code through the shared product validator', () => {
    const original = new ErrorEnvelopeError(createProductErrorEnvelope({
      code: 'COMMIT_NOTHING_STAGED',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
      retryable: false,
    }));

    const decoded = rebuildCataloguedError(serializeCataloguedError(original));

    expect(decoded.envelope).toMatchObject({
      code: 'COMMIT_NOTHING_STAGED',
      area: 'product',
      message: 'Nothing is staged.',
      hint: 'Stage the intended changes, then retry.',
    });
  });
});
