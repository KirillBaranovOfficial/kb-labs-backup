import type { AdapterManifest } from '@kb-labs/core-platform';

/** Project network requirements from the validated adapter manifest. */
export function projectNetworkRequirement(manifest: AdapterManifest | undefined): boolean {
  return manifest?.networkRequired ?? false;
}
