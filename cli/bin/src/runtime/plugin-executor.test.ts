import { afterEach, describe, expect, it, vi } from 'vitest';
import { AdapterUnavailableError } from '@kb-labs/core-platform';
import { renderUnavailableAdapterError } from './plugin-executor.js';

describe('plugin executor adapter errors', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders an unconfigured capability as one line and marks it handled', () => {
    const errorOutput = vi.spyOn(console, 'error').mockImplementation(() => {});

    const handled = renderUnavailableAdapterError(new AdapterUnavailableError('llm'));

    expect(handled).toBe(true);
    expect(errorOutput).toHaveBeenCalledOnce();
    expect(errorOutput.mock.calls[0]?.[0]).toBe(
      'Service llm is not configured. Configure it with: kb config set platform.adapters.llm @kb-labs/adapters-openai',
    );
    expect(errorOutput.mock.calls[0]?.[0]).not.toContain(' at ');
  });

  it('leaves other errors to the regular plugin execution error path', () => {
    expect(renderUnavailableAdapterError(new Error('other failure'))).toBe(false);
  });
});
