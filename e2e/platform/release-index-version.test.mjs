import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { stablePlatformVersion } from './release-index-version.mjs';
import { readPlatformReleaseConfig } from '../../tools/kb-create/scripts/platform-release-config.mjs';
import { releasePackageNames } from './journey-update-packages.mjs';
import registryManifest from './registry-manifest.json' with { type: 'json' };

test('reads stable platform version from a sealed V2 release index', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  assert.equal(stablePlatformVersion(JSON.parse(readFileSync(fixture, 'utf8'))), '2.120.1');
});

test('workflow entry is a platform member in the release config', () => {
  const config = readPlatformReleaseConfig(join(dirname(fileURLToPath(import.meta.url)), '../../tools/kb-create/config/platform-release.jsonc'));
  assert.equal(config.platformMembers.includes('@kb-labs/workflow-entry'), true);
});

test('workflow resolves as a platform service and platform member, never as a plugin', () => {
  const workflowService = registryManifest.services.find(service => service.id === 'workflow');
  assert.equal(workflowService?.pkg, '@kb-labs/workflow-daemon');
  assert.equal(Object.hasOwn(workflowService ?? {}, 'plugin'), false);
  assert.equal(registryManifest.plugins.some(plugin => plugin.pkg === '@kb-labs/workflow-entry'), false);
  assert.equal(releasePackageNames(registryManifest).includes('@kb-labs/workflow-daemon'), true);
  assert.equal(releasePackageNames(registryManifest).includes('@kb-labs/workflow-entry'), true);

  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  const index = JSON.parse(readFileSync(fixture, 'utf8'));
  assert.equal(index.plugins.some(plugin => plugin.package === '@kb-labs/workflow-entry'), false);
  assert.equal(index.platforms[0].members.some(member => member.package === '@kb-labs/workflow-entry'), true);
  assert.equal(index.platforms[0].members.some(member => member.package === '@kb-labs/workflow-daemon'), true);
});
