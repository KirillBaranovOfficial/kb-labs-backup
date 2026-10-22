import { afterEach, describe, expect, it, vi } from 'vitest';
import { AdapterCredentialsMissingError } from '@kb-labs/sdk';
import { OpenAILLM } from './llm.js';

describe('OpenAILLM credentials', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('exports the adapter manifest from the configured llm subpath', async () => {
    const llm = await import('./llm.js');

    expect(llm.manifest).toMatchObject({ id: 'openai-llm', implements: 'ILLM' });
  });

  // case: S-054.8
  it('fails before sending a completion request when no API key is configured', async () => {
    vi.stubEnv('OPENAI_API_KEY', '');
    const adapter = new OpenAILLM();

    await expect(adapter.complete('secret prompt')).rejects.toBeInstanceOf(AdapterCredentialsMissingError);
  });

  // case: S-054.8
  it('fails before opening a stream when no API key is configured', async () => {
    vi.stubEnv('OPENAI_API_KEY', '');
    const adapter = new OpenAILLM();
    const stream = adapter.stream('secret prompt')[Symbol.asyncIterator]();

    await expect(stream.next()).rejects.toBeInstanceOf(AdapterCredentialsMissingError);
  });

  // case: S-054.8
  it('accepts a configured API key', async () => {
    vi.stubEnv('OPENAI_API_KEY', 'test-key');
    const adapter = new OpenAILLM();
    expect(adapter.getProtocolCapabilities().stream.supported).toBe(true);
  });
});
