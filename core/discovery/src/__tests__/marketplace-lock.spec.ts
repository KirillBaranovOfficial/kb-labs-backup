import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import * as os from 'node:os';
import { readMarketplaceLock, writeMarketplaceLock, createEmptyLock, addToMarketplaceLock, removeFromMarketplaceLock } from '../marketplace-lock.js';
import { DiagnosticCollector } from '../diagnostics.js';

describe('marketplace-lock v3', () => {
  let tmpDir: string;
  beforeEach(async () => { tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-lock-test-')); });
  afterEach(async () => { await fs.rm(tmpDir, { recursive: true, force: true }); });

  it('writes sorted, machine independent project pins', async () => {
    const lock = createEmptyLock();
    lock.plugins['z-plugin'] = { version: '1.2.3', integrity: 'sha512-abc', source: 'marketplace', enabled: true };
    lock.plugins['@scope/a-plugin'] = { version: '4.5.6', integrity: 'sha512-def', source: 'marketplace', enabled: false };
    await writeMarketplaceLock(tmpDir, lock);
    const raw = await fs.readFile(path.join(tmpDir, '.kb', 'marketplace.lock'), 'utf8');
    expect(raw).toContain('"schema": "kb.marketplace/3"');
    expect(Object.keys(JSON.parse(raw).plugins)).toEqual(['@scope/a-plugin', 'z-plugin']);
    expect(raw).not.toMatch(/resolvedPath|installedAt|\/Users\/|[A-Z]:\\/);
    expect(await readMarketplaceLock(tmpDir, new DiagnosticCollector())).toEqual(lock);
  });

  it('rejects v2 with a coded diagnostic and exact regeneration command', async () => {
    await fs.mkdir(path.join(tmpDir, '.kb'), { recursive: true });
    await fs.writeFile(path.join(tmpDir, '.kb', 'marketplace.lock'), JSON.stringify({ schema: 'kb.marketplace/2', installed: {} }));
    const diag = new DiagnosticCollector();
    expect(await readMarketplaceLock(tmpDir, diag)).toBeNull();
    expect(diag.getEvents()[0]?.code).toBe('LOCK_SCHEMA_INVALID');
    expect(diag.getEvents()[0]?.remediation).toContain('kb marketplace sync');
  });

  it('rejects machine-specific paths in project pins', async () => {
    await fs.mkdir(path.join(tmpDir, '.kb'), { recursive: true });
    await fs.writeFile(path.join(tmpDir, '.kb', 'marketplace.lock'), JSON.stringify({
      schema: 'kb.marketplace/3', plugins: { demo: { version: '1.0.0', integrity: 'sha512-x', enabled: true, source: 'marketplace', resolvedPath: '/machine/store/demo' } },
    }));
    const diag = new DiagnosticCollector();
    expect(await readMarketplaceLock(tmpDir, diag)).toBeNull();
    expect(diag.getEvents()[0]?.code).toBe('LOCK_SCHEMA_INVALID');
  });

  it('mutates pins without changing a disabled entry and removes pins', async () => {
    await addToMarketplaceLock(tmpDir, 'demo', { version: '1.0.0', integrity: 'sha512-x', source: 'marketplace', enabled: false });
    await addToMarketplaceLock(tmpDir, 'demo', { version: '1.0.0', integrity: 'sha512-x', source: 'marketplace', enabled: true });
    const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
    expect(lock?.plugins.demo?.enabled).toBe(false);
    expect(await removeFromMarketplaceLock(tmpDir, 'demo')).toBe(true);
  });
});
