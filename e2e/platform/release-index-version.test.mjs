import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { stablePlatformVersion } from './release-index-version.mjs';

test('reads stable platform version from a sealed V2 release index', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  assert.equal(stablePlatformVersion(JSON.parse(readFileSync(fixture, 'utf8'))), '2.120.1');
});
