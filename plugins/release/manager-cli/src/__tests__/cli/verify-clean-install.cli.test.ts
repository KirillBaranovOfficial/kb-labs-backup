import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@kb-labs/release-manager-core', () => ({
  verifyCleanInstall: vi.fn(),
}));

import { verifyCleanInstall } from '@kb-labs/release-manager-core';
import { createCapturedUI, createMockContext, mockCLIInput } from '@kb-labs/sdk/testing';
import verifyCleanInstallCommand from '../../cli/commands/verify-clean-install.js';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('release:clean-install', () => {
  it('VCI-01: ok:true when verifyCleanInstall succeeds', async () => {
    vi.mocked(verifyCleanInstall).mockResolvedValue({ ok: true });
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    const result = await verifyCleanInstallCommand.execute(
      ctx as never,
      mockCLIInput({ flags: { tarball: '/tmp/pkg.tgz', name: '@kb-labs/sdk' } }),
    );

    expect(result.ok).toBe(true);
    expect(vi.mocked(verifyCleanInstall)).toHaveBeenCalledWith('/tmp/pkg.tgz', '@kb-labs/sdk', [], 'npm', undefined);
  });

  it('VCI-05: passes --registry through to verifyCleanInstall', async () => {
    vi.mocked(verifyCleanInstall).mockResolvedValue({ ok: true });
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    await verifyCleanInstallCommand.execute(
      ctx as never,
      mockCLIInput({ flags: { tarball: '/tmp/pkg.tgz', name: '@kb-labs/sdk', registry: 'http://localhost:4873' } }),
    );

    expect(vi.mocked(verifyCleanInstall)).toHaveBeenCalledWith('/tmp/pkg.tgz', '@kb-labs/sdk', [], 'npm', 'http://localhost:4873');
  });

  it('VCI-02: ok:false with the real reason surfaced when verifyCleanInstall fails', async () => {
    vi.mocked(verifyCleanInstall).mockResolvedValue({
      ok: false,
      error: 'install failed: [EUNSUPPORTEDPROTOCOL] Unsupported URL Type "workspace:": workspace:*',
    });
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    const result = await verifyCleanInstallCommand.execute(
      ctx as never,
      mockCLIInput({ flags: { tarball: '/tmp/pkg.tgz', name: '@kb-labs/sdk' } }),
    );

    expect(result.ok).toBe(false);
    if (result.ok) { throw new Error('expected clean install verification to fail'); }
    expect(result.error.message).toContain('EUNSUPPORTEDPROTOCOL');
    expect(result.error.message).toContain('workspace:*');
  });

  it('VCI-03: missing --tarball or --name fails before calling verifyCleanInstall', async () => {
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    const result = await verifyCleanInstallCommand.execute(ctx as never, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(false);
    expect(vi.mocked(verifyCleanInstall)).not.toHaveBeenCalled();
  });

  it('VCI-04: --json outputs the CleanInstallResult', async () => {
    vi.mocked(verifyCleanInstall).mockResolvedValue({ ok: true });
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    const result = await verifyCleanInstallCommand.execute(
      ctx as never,
      mockCLIInput({ flags: { tarball: '/tmp/pkg.tgz', name: '@kb-labs/sdk', json: true } }),
    );

    expect(result.ok).toBe(true);
    const out = result.result as Record<string, unknown>;
    expect(out.ok).toBe(true);
  });
});
