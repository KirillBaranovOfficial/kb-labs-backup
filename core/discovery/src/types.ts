/**
 * @module @kb-labs/core-discovery/types
 * Core types for marketplace-based entity discovery.
 */

import type { ManifestV3 } from '@kb-labs/plugin-contracts';

// ---------------------------------------------------------------------------
// Entity Signature (platform-issued proof of quality)
// ---------------------------------------------------------------------------

export interface EntitySignature {
  /** Signing algorithm (e.g., 'ed25519', 'sha256-rsa') */
  algorithm: string;
  /** Base64-encoded signature bytes */
  value: string;
  /** Identity of the signer (e.g., 'kb-labs-platform') */
  signer: string;
  /** ISO timestamp of when the signature was created */
  signedAt: string;
  /** List of checks the entity passed (e.g., ['integrity', 'types', 'lint', 'tests']) */
  verifiedChecks: string[];
}

/** Structured answer from a configured Marketplace identity source. */
export interface MarketplaceResolvedIdentity {
  /** Package manager coordinate used for storage and removal. */
  id: string;
  version: string;
  integrity: string;
  signature?: EntitySignature;
  primaryKind?: EntityKind;
  downloadUrl?: string;
  /** Registry canonical signer subject, e.g. author-handle/package-name. */
  signatureSubject?: string;
}

export type MarketplaceIdentityResolution =
  | { status: 'found'; package: MarketplaceResolvedIdentity }
  | { status: 'not-found-unowned' }
  | { status: 'not-found-owned' };

// ---------------------------------------------------------------------------
// Entity Kind (extensible)
// ---------------------------------------------------------------------------

export type EntityKind =
  | 'plugin'
  | 'adapter'
  | 'cli-command'
  | 'rest-route'
  | 'ws-channel'
  | 'workflow'
  | 'webhook'
  | 'job'
  | 'cron'
  | 'studio-widget'
  | 'studio-menu'
  | 'studio-layout'
  | 'skill'
  | 'hook'
  | (string & {});

// ---------------------------------------------------------------------------
// Marketplace Lock (.kb/marketplace.lock)
// ---------------------------------------------------------------------------

export interface MarketplaceLock {
  schema: 'kb.marketplace/3';
  plugins: Record<string, MarketplaceEntry>;
  /** Machine-only entries; adapters are never project scoped. */
  adapters?: Record<string, MarketplaceAdapterEntry>;
}

declare const userOwnedLinkPathBrand: unique symbol;
declare const installOwnedRelativePathBrand: unique symbol;

/** A user-created pointer; it may be absolute or relative to the lock scope. */
export type UserOwnedLinkPath = string & { readonly [userOwnedLinkPathBrand]: true };

/** A serialized install path that must stay relative to its lock scope. */
export type InstallOwnedRelativePath = string & { readonly [installOwnedRelativePathBrand]: true };

interface MarketplaceEntryMetadata {
  /**
   * Trust level: 'trusted' = signed by KB Labs Registry (sealed, no self-upgrade).
   * 'untrusted' = installed from npm/local/workspace. Default: 'untrusted'.
   */
  trust?: 'trusted' | 'untrusted';
  /** Platform-issued signature (optional, for verified packages) */
  signature?: EntitySignature;
  /** Primary entity kind — discriminator for filtering (e.g., 'plugin', 'adapter') */
  primaryKind?: EntityKind;
  /** All entity kinds this package provides (extracted from manifest) */
  provides?: EntityKind[];
  /** Whether the entity is active (default: true) */
  enabled?: boolean;
}

/** Plugin lock entry; package-manager identity is mandatory for installed pins. */
export type MarketplaceEntry = MarketplaceEntryMetadata & (
  | {
      source: 'marketplace';
      version: string;
      integrity: string;
      package: string;
      resolvedPath?: InstallOwnedRelativePath;
      /** Source in the configured ordered Marketplace source list. */
      marketplaceSource: 'registry' | 'npm';
      /** Trust result from the source that served this artifact. */
      signatureStatus: 'verified' | 'unsigned';
      link?: never;
    }
  | {
      source: 'platform';
      version: string;
      integrity: string;
      package: string;
      resolvedPath: InstallOwnedRelativePath;
      link?: never;
    }
  | {
      source: 'local';
      link: UserOwnedLinkPath;
      package?: never;
      version?: string;
      integrity?: string;
      resolvedPath?: never;
    }
);

/** Machine-local adapter lock entry; adapters use package IDs as their keys. */
export type MarketplaceAdapterEntry =
  | { source: 'platform'; version: string; integrity: string; resolvedPath: InstallOwnedRelativePath; enabled: boolean }
  | { source: 'marketplace'; version: string; integrity: string; resolvedPath: InstallOwnedRelativePath; enabled: boolean; marketplaceSource: 'registry' | 'npm'; signatureStatus: 'verified' | 'unsigned' };

export type MarketplaceLockEntry = MarketplaceEntry | MarketplaceAdapterEntry;

// ---------------------------------------------------------------------------
// Discovery Result
// ---------------------------------------------------------------------------

/** Where an entity lives (ADR-0012): the shared platform install or the user's project. */
export type DiscoveryScope = 'platform' | 'project';

/**
 * Where a discovered plugin came from. Also its shadowing priority when the
 * same plugin id is found more than once in one scope:
 * `workspace` > `linked` > `node_modules`.
 *
 * - `workspace`: a pnpm workspace package under the scope root (monorepo development).
 * - `linked`: a marketplace lock entry with `source: 'local'` (`kb marketplace plugins add`).
 * - `node_modules`: a marketplace lock entry with `source: 'marketplace'` (installed package).
 */
export type DiscoveryOrigin = 'workspace' | 'linked' | 'node_modules';

export interface DiscoveredPlugin {
  /** Plugin identifier (@scope/name) */
  id: string;
  /** Plugin version (semver) */
  version: string;
  /** Path to the package root */
  packageRoot: string;
  /** `name` from package.json (falls back to the plugin id) */
  packageName: string;
  /** Scope the plugin was discovered in */
  scope: DiscoveryScope;
  /** Origin of the plugin, drives shadowing priority */
  origin: DiscoveryOrigin;
  /** File the manifest was read from (static JSON or compiled module) */
  manifestPath: string;
  /** `static`: read from JSON, no plugin code executed. `module`: imported. */
  manifestKind: 'static' | 'module';
  /** How this plugin was installed */
  source: { kind: 'marketplace' | 'local'; path: string };
  /** Display metadata */
  display?: { name?: string; description?: string };
  /** SRI integrity from marketplace.lock */
  integrity?: string;
  /** Platform signature from marketplace.lock */
  signature?: EntitySignature;
  /** Entity kinds extracted from manifest */
  provides: EntityKind[];
}

/** A candidate that was found but could not be turned into a plugin. */
export interface DiscoveryFailure {
  /** Lock key or package name of the candidate */
  id: string;
  packageName: string;
  packageRoot: string;
  scope: DiscoveryScope;
  origin: DiscoveryOrigin;
  /** File that failed to load, when known */
  manifestPath?: string;
  /** `manifest`: the manifest exists but could not be loaded. */
  reason: 'manifest' | 'integrity' | 'package-missing' | 'package-unavailable';
  message: string;
  diagnosticCode?: DiagnosticCode;
  remediation?: string;
}

export interface DiscoveryResult {
  /** Successfully discovered plugins */
  plugins: DiscoveredPlugin[];
  /** Loaded manifests keyed by plugin ID */
  manifests: Map<string, ManifestV3>;
  /** Candidates that were found but failed to load */
  failures: DiscoveryFailure[];
  /** Diagnostic events from the discovery process */
  diagnostics: DiagnosticEvent[];
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

export type DiagnosticSeverity = 'error' | 'warning' | 'info' | 'debug';

export interface DiagnosticEvent {
  /** Severity level */
  severity: DiagnosticSeverity;
  /** Machine-readable code for programmatic handling */
  code: DiagnosticCode;
  /** Human-readable description of the issue */
  message: string;
  /** Contextual information about the affected entity */
  context?: {
    pluginId?: string;
    entityKind?: EntityKind;
    entityId?: string;
    filePath?: string;
  };
  /** Unix timestamp of when the event occurred */
  ts: number;
  /** Error stack trace (for errors) */
  stack?: string;
  /** Suggested fix for the issue */
  remediation?: string;
}

export type DiagnosticCode =
  | 'LOCK_NOT_FOUND'
  | 'LOCK_PARSE_ERROR'
  | 'LOCK_SCHEMA_INVALID'
  | 'MARKETPLACE_PROVENANCE_REQUIRED'
  | 'MANIFEST_NOT_FOUND'
  | 'MANIFEST_PARSE_ERROR'
  | 'MANIFEST_VALIDATION_ERROR'
  | 'MANIFEST_LOAD_TIMEOUT'
  | 'MANIFEST_NOT_PLUGIN'
  | 'INTEGRITY_MISMATCH'
  | 'SIGNATURE_INVALID'
  | 'SIGNATURE_MISSING'
  | 'DEPENDENCY_MISSING'
  | 'ENTITY_CONFLICT'
  | 'PLUGIN_DISABLED'
  | 'PLUGIN_BLOCKED'
  | 'PLUGIN_NOT_ALLOWED'
  | 'PACKAGE_NOT_FOUND'
  | 'COMMAND_SCHEMA_INVALID'
  | 'COMMAND_HANDLER_MISSING'
  | 'COMMAND_HANDLER_UNREADABLE'
  | 'PLUGIN_ID_MISMATCH'
  | (string & {});
