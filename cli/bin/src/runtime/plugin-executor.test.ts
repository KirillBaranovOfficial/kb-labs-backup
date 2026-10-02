import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { AdapterUnavailableError } from '@kb-labs/core-platform';
import { renderUnavailableAdapterError, withPluginRuntimeEnvironment } from './plugin-executor.js';

describe('plugin executor adapter errors', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders an unconfigured capability as one line and marks it handled', () => {
    const errorOutput = vi.spyOn(console, 'error').mockImplementation(() => {});

    const handled = renderUnavailableAdapterError(new AdapterUnavailableError('llm'));

    expect(handled).toBe(true);
    expect(errorOutput).toHaveBeenCalledOnce();
    expect(errorOutput.mock.calls[0]?.[0]).toBe(
      'Service llm is not configured. Configure it with: kb adapter bind llm @kb-labs/adapters-openai',
    );
    expect(errorOutput.mock.calls[0]?.[0]).not.toContain(' at ');
  });

  it('leaves other errors to the regular plugin execution error path', () => {
    expect(renderUnavailableAdapterError(new Error('other failure'))).toBe(false);
  });

  // case: S-010.7
  it('passes the installed platform gateway address into plugin execution and restores the CLI environment', async () => {
    const platformRoot = await mkdtemp(path.join(os.tmpdir(), 'cli-plugin-gateway-'));
    const receiptDir = path.join(platformRoot, '.kb', 'v2');
    const previous = process.env.KB_GATEWAY_URL;
    delete process.env.KB_GATEWAY_URL;
    try {
      await mkdir(receiptDir, { recursive: true });
      await writeFile(path.join(receiptDir, 'receipt.json'), JSON.stringify({
        verification: { hostHealthUrl: 'http://127.0.0.1:43123/health' },
      }));

      await withPluginRuntimeEnvironment(platformRoot, {}, async () => {
        expect(process.env.KB_GATEWAY_URL).toBe('http://127.0.0.1:43123');
      });
      expect(process.env.KB_GATEWAY_URL).toBeUndefined();
    } finally {
      if (previous === undefined) {
        delete process.env.KB_GATEWAY_URL;
      } else {
        process.env.KB_GATEWAY_URL = previous;
      }
      await rm(platformRoot, { recursive: true, force: true });
    }
  });
});
