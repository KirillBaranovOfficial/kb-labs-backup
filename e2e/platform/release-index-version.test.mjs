import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { stablePlatformVersion } from './release-index-version.mjs';
import { readPlatformReleaseConfig } from '../../tools/kb-create/scripts/platform-release-config.mjs';

test('reads stable platform version from a sealed V2 release index', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  assert.equal(stablePlatformVersion(JSON.parse(readFileSync(fixture, 'utf8'))), '2.120.1');
});

test('journey platform members match the release config and exclude unlisted devkit', () => {
  const config = readPlatformReleaseConfig(join(dirname(fileURLToPath(import.meta.url)), '../../.kb/kb.config.jsonc'));
  assert.deepEqual(config.platformMembers, [
    '@kb-labs/cli-bin',
    '@kb-labs/scaffold',
    '@kb-labs/workflow-entry',
    '@kb-labs/marketplace-entry',
  ]);
  assert.equal(config.platformMembers.includes('@kb-labs/devkit'), false);
});
