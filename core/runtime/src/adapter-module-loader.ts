import path from 'node:path';
import { promises as fs } from 'node:fs';
import { pathToFileURL } from 'node:url';
import type { DiscoveredAdapter } from './discover-adapters.js';
import type { LoadedAdapterModule } from './adapter-loader.js';
import { AdapterUnavailableError } from '@kb-labs/core-platform';

/** Create a typed startup error with the supported command for a missing binding. */
export function configuredAdapterLoadError(slot: string, modulePath: string): AdapterUnavailableError {
  const packageName = modulePath.split('/').slice(0, 2).join('/');
  return new AdapterUnavailableError(
    slot,
    'load-failed',
    `Configured adapter package "${modulePath}" could not be loaded. ` +
      `Install it with \`kb marketplace install ${packageName} --scope platform\`.`,
  );
}

/** Load an adapter from its discovered package, preserving requested subpaths. */
export async function loadDiscoveredAdapterModule(
  adapter: DiscoveredAdapter,
  modulePath: string,
): Promise<LoadedAdapterModule> {
  const basePackage = modulePath.split('/').slice(0, 2).join('/');
  const subpath = modulePath.startsWith(`${basePackage}/`)
    ? modulePath.slice(basePackage.length + 1)
    : undefined;
  const filePath = subpath
    ? path.join(adapter.pkgRoot, 'dist', `${subpath}.js`)
    : path.join(adapter.pkgRoot, 'dist', 'index.js');

  await fs.access(filePath);
  const module = await import(pathToFileURL(filePath).href);
  const factory = module.createAdapter || module.default;
  const manifest = module.manifest;
  if (typeof factory !== 'function') {
    throw new Error(`${subpath ? `Subpath ${modulePath}` : `Module ${modulePath}`} has no createAdapter or default export`);
  }
  if (!manifest || typeof manifest !== 'object') {
    throw new Error(`${subpath ? `Subpath ${modulePath}` : `Module ${modulePath}`} has no manifest export`);
  }
  return { createAdapter: factory, manifest, pkgRoot: adapter.pkgRoot };
}
