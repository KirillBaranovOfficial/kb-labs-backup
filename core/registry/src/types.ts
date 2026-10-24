import type { ICache } from '@kb-labs/core-platform/adapters';
import type { CatalogOpenOptions, HealthContributor } from '@kb-labs/plugin-contracts';

export interface RegistryOptions {
  healthContributors?: HealthContributor[];
  root?: string;
  platformRoot?: string;
  cache?: { ttlMs?: number; adapter?: ICache; snapshotKey?: string };
  platformVersion?: string;
  importTimeoutMs?: number;
  verifyIntegrity?: boolean;
  workspaceAdapters?: CatalogOpenOptions['workspaceAdapters'];
  persistSnapshot?: boolean;
}
