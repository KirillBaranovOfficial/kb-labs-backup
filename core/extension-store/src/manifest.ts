/**
 * @module @kb-labs/core-extension-store/manifest
 * Static-only manifest projection for a store entry (ADR-0048 D2/D4.6, F4).
 *
 * The store never imports plugin JS: a version's manifest is captured once, at
 * install time, from its build-emitted static `dist/manifest.json` (or the
 * `kb.manifest` package.json field when it already points at
 * a `.json` file). A package without a usable static manifest simply gets no
 * `manifest` field in its entry — enforcing that every entry has one is a
 * follow-up (ADR-0048 R6, task 7.4), not this task.
 */

import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { declaredManifestPath, isManifestV3, type ManifestV3 } from '@kb-labs/plugin-contracts';

function isWithinRoot(filePath: string, root: string): boolean {
  const resolved = path.resolve(filePath);
  const resolvedRoot = path.resolve(root);
  return resolved === resolvedRoot || resolved.startsWith(resolvedRoot + path.sep);
}

async function readJsonManifest(filePath: string): Promise<ManifestV3 | undefined> {
  let raw: string;
  try {
    raw = await fs.readFile(filePath, 'utf-8');
  } catch {
    return undefined;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return undefined;
  }
  return isManifestV3(parsed) ? parsed : undefined;
}

/**
 * Read a package's static manifest without ever executing its code.
 *
 * Resolution order: the package.json `kb.manifest` field when
 * it names a `.json` file (or has a `.js` sibling `.json` build artifact), then
 * the conventional `dist/manifest.json`. Returns `undefined` when no static
 * manifest is found or the field points outside the package (path traversal).
 */
export async function readStaticManifest(packageRoot: string): Promise<ManifestV3 | undefined> {
  const resolvedRoot = path.resolve(packageRoot);

  try {
    const raw = await fs.readFile(path.join(resolvedRoot, 'package.json'), 'utf-8');
    const pkg = JSON.parse(raw) as Record<string, unknown>;
    const manifestField = declaredManifestPath(pkg);

    if (typeof manifestField === 'string') {
      const staticPath = path.resolve(
        resolvedRoot,
        manifestField.endsWith('.json') ? manifestField : manifestField.replace(/\.[cm]?js$/, '.json'),
      );
      if (isWithinRoot(staticPath, resolvedRoot)) {
        const fromField = await readJsonManifest(staticPath);
        if (fromField) {return fromField;}
      }
    }
  } catch {
    // package.json missing or unreadable — fall through to the convention path
  }

  return readJsonManifest(path.join(resolvedRoot, 'dist', 'manifest.json'));
}
