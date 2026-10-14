import { afterEach, describe, expect, it, vi } from 'vitest';
import { createErrorEnvelope } from '@kb-labs/core-platform';
import { TerminalEventRenderer } from './renderer.js';

describe('TerminalEventRenderer execution failures', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders the shared error envelope code, hint, and correlation ID', () => {
    const write = vi.spyOn(process.stderr, 'write').mockReturnValue(true);
    const envelope = createErrorEnvelope('KB_PLUGIN_COMMAND_FAILED', {
      correlationId: 'corr-123',
      details: {
        plugin: '@kb-labs/test-plugin',
        command: 'test run',
        correlationId: 'corr-123',
        failureReason: 'failed unexpectedly',
      },
    });

    new TerminalEventRenderer().render({
      type: 'execution:error',
      requestId: 'req-123',
      executionId: 'exec-123',
      error: envelope,
    });

    const rendered = write.mock.calls.map(([chunk]) => String(chunk)).join('');
    expect(rendered).toContain(envelope.message);
    expect(rendered).toContain(envelope.code);
    expect(rendered).toContain(envelope.hint);
    expect(rendered).toContain('corr-123');
  });
});
