import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { promises as fsp } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { explainBundle, clearCaches } from '../index';
import { useIsolatedProjectRegistry } from './project-registry';

async function copyDir(src: string, dest: string): Promise<void> {
  await fsp.mkdir(dest, { recursive: true });
  const entries = await fsp.readdir(src, { withFileTypes: true });
  for (const entry of entries) {
    const srcPath = path.join(src, entry.name);
    const destPath = path.join(dest, entry.name);
    if (entry.isDirectory()) { await copyDir(srcPath, destPath); }
    else { await fsp.copyFile(srcPath, destPath); }
  }
}

describe('explainBundle', () => {
  let testDir: string;
  let cleanupRegistry: (() => Promise<void>) | undefined;
  const fixtureDir = path.join(__dirname, '../__fixtures__/workspace');

  beforeEach(async () => {
    testDir = await fsp.mkdtemp(path.join(tmpdir(), 'kb-labs-explain-'));
    await copyDir(fixtureDir, testDir);
    cleanupRegistry = await useIsolatedProjectRegistry(testDir);
    clearCaches();
  });

  afterEach(async () => {
    await cleanupRegistry?.();
    cleanupRegistry = undefined;
    await fsp.rm(testDir, { recursive: true, force: true });
    clearCaches();
  });

  it('returns merge trace array', async () => {
    const trace = await explainBundle({ cwd: testDir, product: 'review', profileId: 'default' });
    expect(Array.isArray(trace)).toBe(true);
    expect(trace.length).toBeGreaterThan(0);
  });

  it('each trace entry has source and path fields', async () => {
    const trace = await explainBundle({ cwd: testDir, product: 'review', profileId: 'default' });
    for (const entry of trace) {
      expect(entry).toHaveProperty('source');
      expect(entry).toHaveProperty('path');
    }
  });
});
