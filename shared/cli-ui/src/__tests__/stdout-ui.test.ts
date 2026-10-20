import { afterEach, describe, expect, it, vi } from 'vitest';
import { createBaseStdoutUI, NOOP_PROMPTS } from '../stdout-ui.js';

describe('createBaseStdoutUI error rendering', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders hints with the shared Hint: format', () => {
    const output = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const ui = createBaseStdoutUI(NOOP_PROMPTS, () => undefined);

    ui.error('Plugin @kb-labs/not-published was not found in the registry. [KB_PLUGIN_NOT_FOUND]', {
      hint: 'kb marketplace search',
    });

    expect(output).toHaveBeenCalledOnce();
    expect(output.mock.calls[0]?.[0]).toContain('Hint: kb marketplace search');
    expect(output.mock.calls[0]?.[0]).not.toContain('│  Hint\n');
  });
});
