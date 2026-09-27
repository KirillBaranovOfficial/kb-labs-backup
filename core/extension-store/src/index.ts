/**
 * @module @kb-labs/core-extension-store
 *
 * Shared per-machine store of installed plugin/adapter versions: one immutable
 * directory per exact `name@version` under `$KB_HOME/store`, dependency dedup
 * delegated to a dedicated pnpm content-addressed store, and version
 * resolution that installs on first use. See ADR-0048 (D2).
 *
 * This package is the store only: it does not read or write any project's
 * `.kb/marketplace.lock` (that pin/lock wiring is task 7.3) and does not run
 * `kb plugin gc` (a garbage collector is a follow-up — the store never deletes
 * an entry on its own, so nothing here needs to be "GC-safe" by removing
 * anything).
 */

export {
  STORE_ENTRY_SCHEMA,
  type InstallPackageRequest,
  type PackageInstallOptions,
  type PackageInstallResult,
  type PackageInstaller,
  type ResolvedStoreEntry,
  type StoreEntry,
  type StoreEntryKind,
  type StoreEntryRef,
  type StoreEntrySignature,
} from './types.js';

export {
  ExtensionStoreError,
  IntegrityMismatchError,
  StoreInstallError,
  isExtensionStoreError,
  type ExtensionStoreErrorCode,
} from './errors.js';

export {
  encodeEntryKey,
  entryPackagePath,
  resolveEntryDir,
  resolveStoreRoot,
  storeCasDir,
  storeEntriesDir,
  storeLockPath,
  storeTmpDir,
  type StoreRootOptions,
} from './store-paths.js';

export { parseStoreEntry, readStoreEntry, serializeStoreEntry } from './entry.js';
export { readStaticManifest } from './manifest.js';
export { PnpmInstaller, type PnpmInstallerOptions } from './pnpm-installer.js';
export { createExtensionStore, type ExtensionStoreOptions, type IExtensionStore } from './store.js';
