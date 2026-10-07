import { describe, it, expect } from 'vitest';
import { createBufferedUI } from '../mcp/ui.js';

describe('createBufferedUI', () => {
  it('captures info/success/warn/error/debug output', () => {
    const { ui, getOutput } = createBufferedUI();
    ui.info('hello');
    ui.success('done');
    ui.warn('careful');
    ui.error('boom');
    ui.debug('trace');
    expect(getOutput()).toBe('[info] hello\n[ok] done\n[warn] careful\n[error] boom\n[debug] trace');
  });

  it('stringifies Error objects passed to error()', () => {
    const { ui, getOutput } = createBufferedUI();
    ui.error(new Error('kaboom'));
    expect(getOutput()).toBe('[error] kaboom');
  });

  it('captures raw write and json', () => {
    const { ui, getOutput } = createBufferedUI();
    ui.write('raw');
    ui.json({ a: 1 });
    expect(getOutput()).toBe('raw\n{"a":1}');
  });

  it('captures spinner lifecycle', () => {
    const { ui, getOutput } = createBufferedUI();
    const sp = ui.spinner('working');
    sp.update('still working');
    sp.succeed('finished');
    expect(getOutput()).toBe('[spinner] working\n[spinner] still working\n[ok] finished');
  });

  // case: S-215.1
  it('rejects unanswered prompts instead of returning defaults', async () => {
    const { ui } = createBufferedUI();
    const prompts = [
      ui.confirm('ok?', { defaultValue: true }),
      ui.prompt('name?', { default: 'x' }),
      ui.select('pick', [{ label: 'A', value: 'a' }]),
      ui.multiSelect('pick', [{ label: 'A', value: 'a', checked: true }]),
    ];
    for (const prompt of prompts) {
      await expect(prompt).rejects.toMatchObject({
        code: 'KB_RUNTIME_INPUT_INVALID',
        hint: 'Provide the value as an explicit tool argument, then retry the tool call.',
        cause: expect.stringContaining('cannot be answered during an MCP tool call'),
      });
    }
  });
});
