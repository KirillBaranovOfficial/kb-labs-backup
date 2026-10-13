import { describe, expect, it } from 'vitest';
import { createErrorEnvelope, createProductErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';
import { rebuildCataloguedError, serializeCataloguedError } from './error-codec.js';

describe('worker IPC error codec', () => {
  // case: S-203.2, S-054.5
  it('round-trips the complete platform error envelope', () => {
    const original = new ErrorEnvelopeError(createErrorEnvelope('KB_ADAPTER_CREDENTIALS_MISSING', {
      message: 'The LLM adapter is missing its credential.',
      hint: 'Set OPENAI_API_KEY and retry.',
      cause: 'The platform environment does not contain OPENAI_API_KEY.',
      correlationId: 'corr-credential-1',
      details: { service: 'llm', credential: 'OPENAI_API_KEY', path: 'platform.adapterOptions.llm.apiKey' },
      actions: [{ id: 'configure-key', label: 'Configure API key', command: 'kb config set ...' }],
      docs: 'https://docs.kblabs.ru/llm',
      diagnostics: [{ code: 'CREDENTIAL_MISSING', message: 'OPENAI_API_KEY is unset.', severity: 'error' }],
    }));

    const serialized = serializeCataloguedError(original);
    const decoded = rebuildCataloguedError(serialized);

    expect(decoded.envelope).toEqual(original.envelope);
    expect(serialized).toEqual(original.envelope);
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
