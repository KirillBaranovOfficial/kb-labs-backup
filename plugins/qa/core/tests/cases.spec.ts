import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { computeQACaseCoverage, parseQACatalog, scanQACaseClaims } from '../src/index.js';

const temporaryDirectories: string[] = [];
function fixture(): string {
  const directory = mkdtempSync(join(tmpdir(), 'qa-cases-'));
  temporaryDirectories.push(directory);
  return directory;
}
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }); });

const caseDocument = `---
id: S-010
title: Marketplace install
persona: solo-developer
area: marketplace
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal
Install a plugin.

## Steps
| # | Action | Expected |
|---|---|---|
| S-010.1 | Run install | Plugin appears in the project |
`;

describe('QA case catalog', () => {
  it('parses cases and rejects unknown fields and malformed step ids with file and line', () => {
    const directory = fixture();
    writeFileSync(join(directory, 'S-010.md'), caseDocument);
    const parsed = parseQACatalog(directory);
    expect(parsed.diagnostics).toEqual([]);
    expect(parsed.cases[0]?.steps[0]).toMatchObject({ id: 'S-010.1', expected: 'Plugin appears in the project' });
    writeFileSync(join(directory, 'S-010.md'), caseDocument.replace('surfaces:', 'automation: manual\nsurfaces:').replace('S-010.1', '1'));
    const invalid = parseQACatalog(directory);
    expect(invalid.diagnostics.map(item => item.message)).toEqual(expect.arrayContaining([
      'unknown frontmatter field automation', 'step id 1 must use S-010.<number>',
    ]));
    expect(invalid.diagnostics.every(item => item.file.endsWith('S-010.md') && item.line > 0)).toBe(true);
  });

  it('scans step claims and requires executed non-unit claims for release cases', () => {
    const directory = fixture();
    const catalogDirectory = join(directory, 'catalog');
    mkdirSync(catalogDirectory);
    writeFileSync(join(catalogDirectory, 'S-010.md'), caseDocument);
    const catalog = parseQACatalog(catalogDirectory);
    mkdirSync(join(directory, 'journey'));
    writeFileSync(join(directory, 'journey', 'step.py'), 'step("install", cases=["S-010.1"])\n');
    writeFileSync(join(directory, 'journey', 'flow.spec.ts'), "test('install flow @S-010.1', () => {})\n// case: S-010.1\nit('install unit', () => {})\n");
    const scanned = scanQACaseClaims(directory, catalog.cases);
    expect(scanned.diagnostics).toEqual([]);
    expect(scanned.claims.map(claim => claim.kind).sort()).toEqual(['e2e', 'journey', 'unit']);
    const missing = computeQACaseCoverage(catalog.cases, scanned.claims, [], 'local');
    expect(missing.cases[0]?.steps[0]?.status).toBe('NOT RUN');
    expect(missing.releasePassed).toBe(false);
    const unitOnly = computeQACaseCoverage(catalog.cases, [{
      id: 'S-010.1', stepId: 'S-010.1', kind: 'unit', source: 'case.test.ts', line: 1,
    }], [{
      schema: 'kb.qa-case-run/v1', runId: 'unit-1', mode: 'local', claims: [{ claimId: 'S-010.1', kind: 'unit', status: 'PASS' }],
    }], 'local');
    expect(unitOnly.cases[0]?.steps[0]?.status).toBe('PASS');
    expect(unitOnly.releasePassed).toBe(false);
    const passed = computeQACaseCoverage(catalog.cases, scanned.claims, [{
      schema: 'kb.qa-case-run/v1', runId: 'run-1', mode: 'local', claims: [{ claimId: 'S-010.1', kind: 'journey', status: 'PASS' }],
    }], 'local');
    expect(passed.cases[0]?.statusResult).toBe('PASS');
    expect(passed.releasePassed).toBe(true);
  });
});
