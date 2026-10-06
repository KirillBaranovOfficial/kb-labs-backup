import { afterEach, describe, expect, it, vi } from 'vitest';
import { createJsonPresenter } from './json.js';

describe('JSON presenter envelope', () => {
  afterEach(() => vi.restoreAllMocks());

  // case: S-203.1
  it('writes one success envelope with the command payload inside data', () => {
    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});

    createJsonPresenter().json({ ok: true, plugins: [] });

    expect(stdout).toHaveBeenCalledOnce();
    expect(JSON.parse(String(stdout.mock.calls[0]?.[0]))).toEqual({
      ok: true,
      data: { plugins: [] },
    });
  });

  // case: S-203.2
  it('converts unknown command result codes to a catalogued error envelope', () => {
    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});

    createJsonPresenter().json({
      ok: false,
      error: { code: 'CMD_NOT_FOUND', message: 'Unknown command: workflow lint' },
    });

    const result = JSON.parse(String(stdout.mock.calls[0]?.[0]));
    expect(result).toMatchObject({
      ok: false,
      error: {
        code: 'KB_RUNTIME_UNEXPECTED',
        message: 'An unexpected error occurred.',
        hint: expect.any(String),
        correlationId: expect.any(String),
      },
    });
  });
});
