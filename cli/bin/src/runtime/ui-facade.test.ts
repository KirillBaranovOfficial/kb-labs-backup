import { afterEach, describe, expect, it, vi } from 'vitest';
import { AdapterUnavailableError } from '@kb-labs/core-platform';
import { createCLIUIFacade } from './ui-facade.js';

describe('CLI UI adapter errors', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    delete process.env.KB_OUTPUT_MODE;
  });

  it('renders an unconfigured adapter as one actionable human line', () => {
    const errorOutput = vi.spyOn(console, 'error').mockImplementation(() => {});

    createCLIUIFacade().error(new AdapterUnavailableError('llm'));

    expect(errorOutput).toHaveBeenCalledOnce();
    expect(errorOutput.mock.calls[0]?.[0]).toBe(
      'Service llm is not configured. Configure it with: kb adapter bind llm @kb-labs/adapters-openai',
    );
    expect(errorOutput.mock.calls[0]?.[0]).not.toContain(' at ');
  });

  it('keeps diagnostics on stderr and writes one success envelope in JSON mode', () => {
    process.env.KB_OUTPUT_MODE = 'json';
    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});
    const stderr = vi.spyOn(process.stderr, 'write').mockImplementation(() => true);
    const ui = createCLIUIFacade();

    ui.warn('configuration warning');
    ui.json({ plugins: [] });

    expect(stderr).toHaveBeenCalledOnce();
    expect(stdout).toHaveBeenCalledOnce();
    expect(JSON.parse(String(stdout.mock.calls[0]?.[0]))).toEqual({
      ok: true,
      data: { plugins: [] },
    });
  });
});
