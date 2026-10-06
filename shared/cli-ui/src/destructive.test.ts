import { describe, it, expect, vi } from 'vitest';
import { confirmDestructive, renderDestructiveMessage, type DestructiveAction } from './destructive';

const ACTION: DestructiveAction = {
  action: 'mind drop',
  resource: 'index "code"',
  effect: 'deletes all vectors',
  severity: 'high',
  reversible: true,
  recovery: 're-index from source',
  blastRadius: { count: 100, unit: 'vectors', scope: 'whole index' },
};

function ctx() {
  return { ui: { json: vi.fn(), warn: vi.fn(), error: vi.fn() } };
}

describe('confirmDestructive', () => {
  it('returns null (proceed) when confirmed, emitting nothing', () => {
    const c = ctx();
    expect(confirmDestructive(c, { confirmed: true, action: ACTION })).toBeNull();
    expect(c.ui.json).not.toHaveBeenCalled();
    expect(c.ui.warn).not.toHaveBeenCalled();
  });

  it('blocks with a typed failure for the shared human renderer when not confirmed', () => {
    const c = ctx();
    const result = confirmDestructive(c, { confirmed: false, isJson: false, action: ACTION });
    expect(result).toMatchObject({
      ok: false,
      error: { code: 'KB_RUNTIME_CONFIRMATION_REQUIRED', kind: 'validation' },
      result: { confirmationRequired: true },
    });
    expect(c.ui.warn).not.toHaveBeenCalled();
    expect(c.ui.json).not.toHaveBeenCalled();
  });

  it('returns a machine-readable signal in json mode (agent sees blast radius)', () => {
    const c = ctx();
    const res = confirmDestructive(c, { confirmed: false, isJson: true, action: ACTION });
    expect(res).toMatchObject({
      ok: false,
      error: { code: 'KB_RUNTIME_CONFIRMATION_REQUIRED' },
      result: { confirmationRequired: true },
    });
    expect(c.ui.warn).not.toHaveBeenCalled();
    expect(c.ui.json).not.toHaveBeenCalled();
    const sig = res!.result as Record<string, unknown>;
    expect(sig).toMatchObject({
      ok: false,
      confirmationRequired: true,
      destructive: true,
      irreversible: false,
      reversible: true,
      severity: 'high',
      action: 'mind drop',
      resource: 'index "code"',
      confirmWith: '--yes',
    });
    expect(sig.blastRadius).toEqual({ count: 100, unit: 'vectors', scope: 'whole index' });
    expect(sig.recovery).toBe('re-index from source');
    expect(String(sig.message)).toContain('mind drop');
  });

  it('marks irreversible actions and omits a recovery field when none', () => {
    const c = ctx();
    const res = confirmDestructive(c, {
      confirmed: false,
      isJson: true,
      action: { ...ACTION, reversible: false, recovery: undefined },
    });
    expect(res).not.toBeNull();
    expect(c.ui.json).not.toHaveBeenCalled();
    const sig = res!.result as Record<string, unknown>;
    expect(sig.irreversible).toBe(true);
    expect('recovery' in sig).toBe(false);
  });

  it('does not print errors directly; the shared renderer owns failure output', () => {
    const c = { ui: { error: vi.fn() } };
    const result = confirmDestructive(c, { confirmed: false, isJson: false, action: ACTION });
    expect(result?.error).toMatchObject({ code: 'KB_RUNTIME_CONFIRMATION_REQUIRED', hint: expect.any(String) });
    expect(c.ui.error).not.toHaveBeenCalled();
  });
});

describe('renderDestructiveMessage', () => {
  it('leads with [severity] + recovery when reversible', () => {
    const m = renderDestructiveMessage(ACTION);
    expect(m).toContain('[high]');
    expect(m).not.toContain('IRREVERSIBLE');
    expect(m).toContain('Recovery: re-index from source');
    expect(m).toContain('Confirm with --yes');
  });

  it('leads with IRREVERSIBLE + "No recovery" when not reversible', () => {
    const m = renderDestructiveMessage({ ...ACTION, reversible: false, recovery: undefined });
    expect(m).toContain('⚠ IRREVERSIBLE [high]');
    expect(m).toContain('No recovery — data is permanently lost');
  });

  it('renders blast radius (count · unit · scope)', () => {
    expect(renderDestructiveMessage(ACTION)).toContain('100 vectors · whole index');
  });

  it('honours a custom confirm flag', () => {
    expect(renderDestructiveMessage({ ...ACTION, confirmFlag: '--force' })).toContain('Confirm with --force');
  });
});
