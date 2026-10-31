import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { productInvariantIssues } from './check-product-invariants.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-product-invariants-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(path.join(root, 'docs/product'), { recursive: true });
  await mkdir(path.join(root, 'scripts/checks'), { recursive: true });
  await writeFile(path.join(root, 'scripts/checks/example.mjs'), '');
  return root;
}

const validRule = `## INV-01 — Fixture\n\n**Enforcement:** check name; [example.mjs](../../scripts/checks/example.mjs).\n`;

test('product invariants check accepts an enforcement entry with an existing linked path', async (t) => {
  const root = await fixture(t);
  assert.deepEqual(productInvariantIssues(validRule, root), []);
});

test('product invariants check reports a rule without an Enforcement entry', async (t) => {
  const root = await fixture(t);
  const issues = productInvariantIssues('## INV-01 — Fixture\n\n**Rule:** One rule.\n', root);
  assert.equal(issues.length, 1);
  assert.match(issues[0].message, /Enforcement entry/);
});

test('product invariants check requires Enforcement entries to link a repository path', async (t) => {
  const root = await fixture(t);
  const issues = productInvariantIssues('## INV-01 — Fixture\n\n**Enforcement:** a check name without a path.\n', root);
  assert.equal(issues.length, 1);
  assert.match(issues[0].message, /must link to a repository path/);
});

test('product invariants check reports a linked enforcement path that does not exist', async (t) => {
  const root = await fixture(t);
  const markdown = '## INV-01 — Fixture\n\n**Enforcement:** [missing.mjs](../../scripts/checks/missing.mjs).\n';
  const issues = productInvariantIssues(markdown, root);
  assert.equal(issues.length, 1);
  assert.match(issues[0].message, /does not exist/);
});
