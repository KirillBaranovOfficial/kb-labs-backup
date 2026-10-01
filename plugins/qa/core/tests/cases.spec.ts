import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import type { QACase, QACaseClaim, QACaseRunResult } from '@kb-labs/qa-contracts';
import { computeQACaseCoverage, parseQACatalog, scanQACaseClaims } from '../src/index.js';

const temporaryDirectories: string[] = [];
function fixture(): string {
  const directory = mkdtempSync(join(tmpdir(), 'qa-cases-'));
  temporaryDirectories.push(directory);
  return directory;
}
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }); });

function caseDocument(id = 'S-010', extra = '', status = 'active', modes = '[local, secured]'): string {
  return `---\nid: ${id}\ntitle: Marketplace install\npersona: solo-developer\narea: marketplace\npriority: P0\nstatus: ${status}\nrelease: true\nmodes: ${modes}\nsurfaces: [cli, studio]\n${extra}---\n\n## Goal\nInstall a plugin.\n\n## Steps\n| # | Action | Expected |\n|---|---|---|\n| ${id}.1 | Run install | Plugin appears in the project |\n| ${id}.2 | Verify plugin | Plugin command works |\n`;
}

function parsedCase(directory: string, id = 'S-010', document = caseDocument(id)): QACase {
  writeFileSync(join(directory, `${id}.md`), document);
  const parsed = parseQACatalog(directory);
  expect(parsed.diagnostics).toEqual([]);
  return parsed.cases[0]!;
}

function run(mode: QACaseRunResult['mode'], claimId: string, status: 'PASS' | 'FAIL', kind: QACaseRunResult['claims'][number]['kind'] = 'journey'): QACaseRunResult {
  return { schema: 'kb.qa-case-run/v1', runId: `${mode}-${status}`, mode, claims: [{ claimId, kind, status }] };
}

describe('QA case catalog diagnostics', () => {
  it('reports file and line for missing fields, duplicate and mismatched steps, invalid enums, and unknown supersedes', () => {
    const directory = fixture();
    const invalid = caseDocument().replace('persona: solo-developer\n', '')
      .replace('status: active', 'status: retired').replace('modes: [local, secured]', 'modes: [remote]')
      .replace('| S-010.2 | Verify plugin | Plugin command works |', '| S-010.1 | Verify plugin | Plugin command works |\n| S-011.3 | Wrong case | Not valid |')
      .replace('surfaces:', 'superseded_by: S-999\nsurfaces:');
    writeFileSync(join(directory, 'S-010.md'), invalid);
    const result = parseQACatalog(directory);
    const diagnostics = result.diagnostics;
    for (const expected of [
      'missing required frontmatter field persona', 'duplicate step id S-010.1',
      'step id S-011.3 must use S-010.<number>', 'status must be active, planned, or historical',
      'modes must contain local and/or secured',
    ]) expect(diagnostics.some(item => item.message === expected)).toBe(true);
    expect(diagnostics.every(item => item.file.endsWith('S-010.md') && item.line > 0)).toBe(true);
    writeFileSync(join(directory, 'S-010.md'), caseDocument('S-010', 'superseded_by: S-999\n', 'historical').replace(/\n## Steps[\s\S]*$/, '\n'));
    const unknownTarget = parseQACatalog(directory).diagnostics;
    expect(unknownTarget).toEqual([expect.objectContaining({ file: expect.stringContaining('S-010.md'), line: 11, message: 'superseded_by references unknown case S-999' })]);
  });

  it('parses active cases and allows step-free historical design records', () => {
    const directory = fixture();
    writeFileSync(join(directory, 'S-010.md'), caseDocument());
    const historical = caseDocument('S-011', 'superseded_by: S-010\n', 'historical').replace(/\n## Steps[\s\S]*$/, '\n');
    writeFileSync(join(directory, 'S-011.md'), historical);
    const parsed = parseQACatalog(directory);
    expect(parsed.diagnostics).toEqual([]);
    expect(parsed.cases.find(item => item.id === 'S-011')).toMatchObject({ supersededBy: 'S-010', steps: [] });
  });
});

describe('QA case claim scanning', () => {
  it('scans journey, Playwright, and adjacent unit claims and rejects unknown ids', () => {
    const directory = fixture();
    const testCase = parsedCase(directory);
    const source = join(directory, 'claims');
    mkdirSync(source);
    const journeyCases = ['cases', '=', '[', '"S-010.1"', ']'].join('');
    const playwrightTest = ['test', '(', "'install flow ", '@', 'S-010.2', "', () => {})"].join('');
    const unitCase = ['/', '/', ' case: S-010.1'].join('');
    writeFileSync(join(source, 'journey.py'), `step("install", ${journeyCases})\n`);
    writeFileSync(join(source, 'flow.spec.ts'), `${playwrightTest}\n${unitCase}\nit('unit install', () => {})\n`);
    const goCase = ['/', '/', ' case: S-010.2'].join('');
    writeFileSync(join(source, 'claim_test.go'), `package claims\n${goCase}\nfunc TestInstall(t *testing.T) {}\n`);
    const unknownStep = `S-${'999'}.1`;
    writeFileSync(join(source, 'invalid.ts'), `test('missing @${unknownStep}', () => {})\n`);
    const scanned = scanQACaseClaims(directory, [testCase]);
    expect(scanned.claims.map(claim => claim.kind).sort()).toEqual(['e2e', 'e2e', 'journey', 'unit', 'unit']);
    expect(scanned.claims.map(claim => claim.stepId).sort()).toEqual(['S-010.1', 'S-010.1', 'S-010.2', 'S-010.2', `S-${'999'}.1`]);
    expect(scanned.diagnostics).toEqual([expect.objectContaining({ file: expect.stringContaining('invalid.ts'), message: 'claim references unknown case S-999' })]);
  });
});

describe('QA case coverage', () => {
  it('filters by mode and marks planned or historical cases NOT REQUIRED', () => {
    const directory = fixture();
    const active = parsedCase(directory);
    const planned = { ...active, id: 'S-011', status: 'planned' as const, release: true, modes: ['secured' as const], steps: active.steps.map(step => ({ ...step, id: step.id.replace('S-010', 'S-011') })) };
    const historical = { ...planned, id: 'S-012', status: 'historical' as const };
    const modeOnly = { ...active, id: 'S-013', modes: ['secured' as const], steps: active.steps.map(step => ({ ...step, id: step.id.replace('S-010', 'S-013') })) };
    const result = computeQACaseCoverage([active, planned, historical, modeOnly], [], [], 'local');
    expect(result.cases[0]?.steps[0]).toMatchObject({ status: 'NOT COVERED', reason: expect.any(String) });
    expect(result.cases[1]).toMatchObject({ statusResult: 'NOT REQUIRED', reason: expect.any(String) });
    expect(result.cases[2]).toMatchObject({ statusResult: 'NOT REQUIRED', reason: expect.any(String) });
    expect(result.cases[3]).toMatchObject({ statusResult: 'NOT REQUIRED', reason: expect.stringContaining('local') });
    expect(result.cases.every(item => item.reason || item.statusResult === 'NOT COVERED')).toBe(true);
    expect(result.releasePassed).toBe(false);
  });

  it('gives an executed failure precedence, reports unrun claims, and rejects unit-only release coverage', () => {
    const directory = fixture();
    const testCase = parsedCase(directory);
    const journey: QACaseClaim = { id: 'S-010.1', stepId: 'S-010.1', kind: 'journey', source: 'journey.py', line: 1 };
    const unit: QACaseClaim = { id: 'S-010.1', stepId: 'S-010.1', kind: 'unit', source: 'case.test.ts', line: 1 };
    const failed = computeQACaseCoverage([testCase], [journey], [run('local', 'S-010.1', 'FAIL')], 'local');
    expect(failed.cases[0]?.steps[0]).toMatchObject({ status: 'FAIL', reason: expect.stringContaining('failed') });
    const notRun = computeQACaseCoverage([testCase], [journey], [], 'local');
    expect(notRun.cases[0]?.steps[0]).toMatchObject({ status: 'NOT RUN', reason: expect.any(String) });
    const unitOnly = computeQACaseCoverage([testCase], [unit], [run('local', 'S-010.1', 'PASS', 'unit')], 'local');
    expect(unitOnly.cases[0]?.steps[0]).toMatchObject({ status: 'NOT COVERED', reason: expect.stringContaining('journey/e2e') });
    expect(unitOnly.cases[0]?.statusResult).toBe('NOT COVERED');
    expect(unitOnly.releasePassed).toBe(false);
  });

  it('passes only when every active release step has a passing journey result for that mode', () => {
    const directory = fixture();
    const testCase = parsedCase(directory);
    const claims = testCase.steps.map(step => ({ id: step.id, stepId: step.id, kind: 'journey' as const, source: 'journey.py', line: 1 }));
    const securedPass = run('secured', 'S-010.1', 'PASS');
    const result = computeQACaseCoverage([testCase], claims, [securedPass], 'local');
    expect(result.cases[0]?.statusResult).toBe('NOT RUN');
    expect(result.releasePassed).toBe(false);
    const complete = computeQACaseCoverage([testCase], claims, testCase.steps.map(step => run('local', step.id, 'PASS')), 'local');
    expect(complete.cases[0]?.statusResult).toBe('PASS');
    expect(complete.releasePassed).toBe(true);
  });
});
