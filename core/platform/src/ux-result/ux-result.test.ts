import { readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { isUxResult, validateUxResult } from './index.js';

const repositoryRoot = resolve(fileURLToPath(new URL('../../../..', import.meta.url)));
const fixturesRoot = join(repositoryRoot, 'contracts/ux-result/fixtures');

function loadFixtures(kind: 'valid' | 'invalid'): Array<{ name: string; value: unknown }> {
  const directory = join(fixturesRoot, kind);
  return readdirSync(directory)
    .filter((file) => file.endsWith('.json'))
    .sort()
    .map((file) => ({ name: file, value: JSON.parse(readFileSync(join(directory, file), 'utf8')) as unknown }));
}

describe('shared UX-result fixtures', () => {
  for (const { name, value } of loadFixtures('valid')) {
    // case: S-203.5
    it(`accepts valid/${name}`, () => {
      expect(validateUxResult(value)).toEqual([]);
      expect(isUxResult(value)).toBe(true);
    });
  }

  for (const { name, value } of loadFixtures('invalid')) {
    // case: S-203.5
    it(`rejects invalid/${name}`, () => {
      expect(validateUxResult(value).length).toBeGreaterThan(0);
      expect(isUxResult(value)).toBe(false);
    });
  }
});
