import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { existsSync, mkdtempSync, realpathSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { resolveRuntimeStatePath } from '@kb-labs/sdk/adapters';
import { SQLiteAnalytics } from './index.js';

const context = { source: { product: 'test', version: '1.0.0' }, runId: 'run-test' };

let project: string;
let kbHome: string;
let previousKbHome: string | undefined;

beforeEach(() => {
  project = realpathSync(mkdtempSync(join(tmpdir(), 'kb-analytics-project-')));
  kbHome = realpathSync(mkdtempSync(join(tmpdir(), 'kb-analytics-home-')));
  previousKbHome = process.env.KB_HOME;
  process.env.KB_HOME = kbHome;
});

afterEach(() => {
  if (previousKbHome === undefined) {
    delete process.env.KB_HOME;
  } else {
    process.env.KB_HOME = previousKbHome;
  }
  rmSync(project, { recursive: true, force: true });
  rmSync(kbHome, { recursive: true, force: true });
});

describe('SQLiteAnalytics default path (ADR-0044)', () => {
  it('lives in the project state directory when no path is configured', () => {
    const db = new SQLiteAnalytics({ workspace: { cwd: project }, context });
    db.close();

    expect(existsSync(resolveRuntimeStatePath(project, ['analytics', 'analytics.sqlite']))).toBe(true);
    expect(existsSync(join(project, '.kb'))).toBe(false);
  });

  it('uses an explicit absolute path without deriving it from the project workspace', () => {
    const externalPath = join(kbHome, 'platform-data', 'analytics.sqlite');
    const db = new SQLiteAnalytics({ workspace: { cwd: project }, dbPath: externalPath, context });
    db.close();

    expect(existsSync(externalPath)).toBe(true);
    expect(existsSync(resolveRuntimeStatePath(project, ['analytics']))).toBe(false);
  });
});
