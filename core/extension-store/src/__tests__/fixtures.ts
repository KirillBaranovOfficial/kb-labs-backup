/**
 * Test-only helpers: a throw-away, offline npm package installed via a
 * `file:` spec, so `PnpmInstaller` tests exercise a real `pnpm install`
 * without any network access.
 */

import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import type { ManifestV3 } from '@kb-labs/plugin-contracts';

export interface FixturePackageOptions {
  name: string;
  version: string;
  /** Written to `dist/manifest.json` when given (a static manifest, never imported). */
  manifest?: ManifestV3;
}

/** Creates a minimal, dependency-free npm package under a fresh temp directory. Returns its absolute path. */
export async function createFixturePackage(options: FixturePackageOptions): Promise<string> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-extension-store-fixture-'));
  await fs.writeFile(
    path.join(dir, 'package.json'),
    JSON.stringify({ name: options.name, version: options.version, private: true }, null, 2),
  );
  if (options.manifest) {
    await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
    await fs.writeFile(path.join(dir, 'dist', 'manifest.json'), JSON.stringify(options.manifest, null, 2));
  }
  return dir;
}

export function sampleManifest(id: string, version: string): ManifestV3 {
  return {
    schema: 'kb.plugin/3',
    id,
    version,
  } as ManifestV3;
}
