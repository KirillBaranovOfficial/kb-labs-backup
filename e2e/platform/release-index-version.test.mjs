import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { stablePlatformVersion } from './release-index-version.mjs';
import { readPlatformReleaseConfig } from '../../tools/kb-create/scripts/platform-release-config.mjs';

test('reads stable platform version from a sealed V2 release index', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  const index = JSON.parse(readFileSync(fixture, 'utf8'));
  assert.equal(stablePlatformVersion(index), index.platforms.find(({ id }) => id === 'platform').version);
});

test('release index contains configured platform members and excludes unlisted devkit', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  const index = JSON.parse(readFileSync(fixture, 'utf8'));
  const config = readPlatformReleaseConfig(join(dirname(fileURLToPath(import.meta.url)), '../../tools/kb-create/config/platform-release.jsonc'));
  const memberPackages = index.platforms.find(({ id }) => id === 'platform').members.map(({ package: packageName }) => packageName);
  assert.ok(config.platformMembers.every(packageName => memberPackages.includes(packageName)));
  assert.equal(memberPackages.includes('@kb-labs/devkit'), false);
});

test('release index config requirements target services in the default graph', () => {
  const fixture = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'release-index-v2.json');
  const platform = JSON.parse(readFileSync(fixture, 'utf8')).platforms.find(({ id }) => id === 'platform');
  const serviceIds = new Set(platform.profiles.default.services.map(({ id }) => id));
  for (const member of platform.members) {
    for (const requirement of member.config ?? []) {
      for (const serviceId of requirement.services ?? []) {
        assert.ok(serviceIds.has(serviceId), `${member.package} requirement ${requirement.id} targets absent service ${serviceId}`);
      }
    }
  }
});
