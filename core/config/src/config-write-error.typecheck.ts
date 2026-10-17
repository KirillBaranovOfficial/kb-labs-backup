import { ConfigWriteError } from './user-config/write-user-config.js';

new ConfigWriteError('KB_CONFIG_INVALID', 'invalid project setting', {
  path: 'someSection.key',
  nextCommand: 'kb config set plugins.someSection.key 1',
});

// @ts-expect-error Internal metadata cannot be emitted as a catalogue detail.
new ConfigWriteError('KB_CONFIG_INVALID', 'invalid project setting', { path: 'someSection.key', reason: 'project-scope-path' });

// @ts-expect-error KB_CONFIG_INVALID requires its message and hint template field.
new ConfigWriteError('KB_CONFIG_INVALID', 'invalid project setting', { nextCommand: 'kb config set plugins.someSection.key 1' });
