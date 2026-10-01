import test from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { readPlatformReleaseConfig } from './platform-release-config.mjs';

test('the platform release config binds every persistent built-in used by a fresh install', () => {
  const configPath = fileURLToPath(new URL('../../../.kb/kb.config.jsonc', import.meta.url));
  const config = readPlatformReleaseConfig(configPath);
  assert.deepEqual(config.platformRequires, Object.keys(config.adapters));
  for (const slot of [
    'storage', 'cache', 'eventBus', 'documentDatabase', 'kvStore', 'analytics', 'logger',
    'logRingBuffer', 'logPersistence', 'serviceTransport',
  ]) {
    assert.equal(typeof config.adapters[slot], 'string', `${slot} must have a fresh-install binding`);
    assert.ok(config.platformRequires.includes(slot), `${slot} must be required in the release index`);
  }
  assert.equal(config.adapters.documentDatabase, '@kb-labs/adapters-sqlite');
  assert.equal(config.adapters.eventBus, '@kb-labs/adapters-eventbus-cache');
  assert.equal(config.adapters.kvStore, '@kb-labs/adapters-sqlite/kv');
  assert.equal(config.adapterOptions.documentDatabase.filename, '.kb/database/kb.sqlite');
  assert.equal(config.adapterOptions.kvStore.filename, '.kb/database/kb.sqlite');
});
