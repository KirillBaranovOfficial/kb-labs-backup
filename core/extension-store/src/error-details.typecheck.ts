import { IntegrityMismatchError, StoreInstallError } from './errors.js';

new IntegrityMismatchError({ plugin: '@kb-labs/example', expected: 'sha512-a', actual: 'sha512-b' });
new StoreInstallError({ plugin: '@kb-labs/example' }, 'registry unavailable');

// @ts-expect-error Integrity details are derived from KB_PLUGIN_INTEGRITY_MISMATCH.
new IntegrityMismatchError({ plugin: '@kb-labs/example', expected: 'sha512-a', actual: 'sha512-b', reason: 'mismatch' });
