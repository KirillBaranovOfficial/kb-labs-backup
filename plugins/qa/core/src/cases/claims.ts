import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import type { QACase, QACaseClaim, QACaseClaimKind } from '@kb-labs/qa-contracts';
import type { QACatalogDiagnostic } from './catalog.js';

const CLAIM_ID = /^(?:S|PC)-\d{3}\.\d+$/;
const SKIP = new Set(['.git', 'node_modules', 'dist', 'build', '.next', '.kb', '.venv', '.tox', '.pytest_cache', 'vendor', 'coverage']);
const SOURCE_EXTENSIONS = new Set(['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.go', '.py']);

function filesUnder(root: string): string[] {
  const result: string[] = [];
  const visit = (directory: string) => {
    let entries;
    try { entries = readdirSync(directory, { withFileTypes: true }); } catch { return; }
    for (const entry of entries) {
      if (entry.isDirectory() && !SKIP.has(entry.name)) {visit(join(directory, entry.name));}
      else if (entry.isFile() && SOURCE_EXTENSIONS.has(entry.name.slice(entry.name.lastIndexOf('.')))) {result.push(join(directory, entry.name));}
    }
  };
  visit(root);
  return result.sort();
}

function validateClaim(claim: QACaseClaim, cases: QACase[], file: string, line: number, diagnostics: QACatalogDiagnostic[]): void {
  const [caseId] = claim.id.split('.');
  const testCase = cases.find(item => item.id === caseId);
  if (!testCase) { diagnostics.push({ file, line, message: `claim references unknown case ${caseId}` }); return; }
  if (!testCase.steps.some(step => step.id === claim.id)) {diagnostics.push({ file, line, message: `claim references unknown step ${claim.id}` });}
}

export function scanQACaseClaims(root: string, cases: QACase[]): { claims: QACaseClaim[]; diagnostics: QACatalogDiagnostic[] } {
  const claims: QACaseClaim[] = [];
  const diagnostics: QACatalogDiagnostic[] = [];
  for (const filePath of filesUnder(root)) {
    const source = readFileSync(filePath, 'utf8');
    const lines = source.split(/\r?\n/);
    const file = relative(process.cwd(), filePath);
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index] ?? '';
      const add = (stepId: string, kind: QACaseClaimKind, claimLine = index + 1, testName?: string) => {
        if (!CLAIM_ID.test(stepId)) { diagnostics.push({ file, line: claimLine, message: `invalid case step claim ${stepId}` }); return; }
        const claim = { id: stepId, stepId, kind, source: file, line: claimLine, ...(testName ? { testName } : {}) };
        validateClaim(claim, cases, file, claimLine, diagnostics);
        claims.push(claim);
      };
      // Python journey and TypeScript journey helpers both serialize this declaration.
      const casesMatch = line.match(/\bcases\s*=\s*\[([^\]]*)\]/);
      if (casesMatch) {
        for (const match of (casesMatch[1] ?? '').matchAll(/["']((?:S|PC)-\d{3}\.\d+)["']/g)) {add(match[1]!, 'journey');}
      }
      // Playwright titles carry tags at the title or describe block boundary.
      if (/\b(?:test|it)\s*\(/.test(line)) {
        const name = line.match(/\b(?:test|it)\s*\(\s*["'`]([^"'`]+)/)?.[1];
        for (const match of line.matchAll(/@((?:S|PC)-\d{3}\.\d+)/g)) {add(match[1]!, 'e2e', index + 1, name);}
      }
      // A unit test claim must sit immediately above a test declaration.
      const caseComment = line.match(/^\s*\/\/\s*case:\s*((?:S|PC)-\d{3}\.\d+)\s*$/);
      if (caseComment && filePath.endsWith('.go')) {
        const next = lines[index + 1] ?? '';
        const testLine = next.match(/^\s*func\s+(Test\w+)/);
        if (!testLine) {diagnostics.push({ file, line: index + 1, message: '// case: must be directly above a Go test function' });}
        else {add(caseComment[1]!, 'unit', index + 1, testLine[1]!);}
      } else if (caseComment) {
        const next = lines[index + 1] ?? '';
        const testLine = next.match(/\b(?:test|it)\s*\(\s*["'`]([^"'`]+)/);
        if (!testLine) {diagnostics.push({ file, line: index + 1, message: '// case: must be directly above a test declaration' });}
        else {add(caseComment[1]!, 'unit', index + 1, testLine[1]!);}
      }
    }
  }
  return { claims, diagnostics };
}
