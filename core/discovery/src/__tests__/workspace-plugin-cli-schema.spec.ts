import { readFile, readdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { DiagnosticCollector } from '../diagnostics.js';
import { loadManifestFile } from '../manifest-loader.js';
import { CliCommandDeclSchema } from '@kb-labs/plugin-contracts';

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');

async function findPackageFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files: string[] = [];
  for (const entry of entries) {
    if (entry.name === 'node_modules' || entry.name === 'dist') { continue; }
    const fullPath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await findPackageFiles(fullPath));
    } else if (entry.name === 'package.json') {
      files.push(path.relative(repositoryRoot, fullPath));
    }
  }
  return files;
}

async function declaresPluginV3(packageRoot: string): Promise<boolean> {
  const sourceRoot = path.join(packageRoot, 'src');
  async function findManifestSources(directory: string): Promise<string[]> {
    const entries = await readdir(directory, { withFileTypes: true });
    const files: string[] = [];
    for (const entry of entries) {
      if (entry.name === 'node_modules' || entry.name === 'dist') { continue; }
      const fullPath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        files.push(...await findManifestSources(fullPath));
      } else if (/^manifest(?:\.v3)?\.(?:ts|js)$/.test(entry.name)) {
        files.push(fullPath);
      }
    }
    return files;
  }
  if (!existsSync(sourceRoot)) { return false; }
  const sourceFiles = await findManifestSources(sourceRoot);
  for (const sourceFile of sourceFiles) {
    const content = await readFile(sourceFile, 'utf8');
    if (/schema\s*:\s*['"]kb\.plugin\/3['"]/.test(content)) { return true; }
  }
  return false;
}

describe('first-party plugin CLI declarations', () => {
  // case: S-203.18
  it('validates every built kb.plugin/3 workspace manifest using discovery validation', async () => {
    const packageFiles = await findPackageFiles(path.join(repositoryRoot, 'plugins'));
    const failures: string[] = [];
    let manifestsChecked = 0;

    for (const packageFile of packageFiles) {
      const packageRoot = path.dirname(path.join(repositoryRoot, packageFile));
      const packageJson = JSON.parse(await readFile(path.join(packageRoot, 'package.json'), 'utf8')) as {
        name?: string;
        kbLabs?: { manifest?: string };
        kb?: { manifest?: string };
      };
      if (!packageJson.kbLabs?.manifest && !packageJson.kb?.manifest) { continue; }
      if (!await declaresPluginV3(packageRoot)) { continue; }

      const diagnostic = new DiagnosticCollector();
      const loaded = await loadManifestFile(packageRoot, diagnostic);
      if (!loaded) {
        failures.push(`${packageJson.name ?? packageRoot}: manifest could not be loaded (${diagnostic.getEvents().map(event => event.code).join(', ')})`);
        continue;
      }
      expect(loaded.manifest.schema, `${packageJson.name} must load a kb.plugin/3 manifest`).toBe('kb.plugin/3');
      manifestsChecked += 1;
      for (const command of loaded.manifest.cli?.commands ?? []) {
        const parsed = CliCommandDeclSchema.safeParse(command);
        if (!parsed.success) {
          failures.push(`${packageJson.name ?? packageRoot}:${command.path}: ${parsed.error.issues.map(issue => issue.message).join('; ')}`);
        }
      }
    }

    expect(manifestsChecked).toBeGreaterThan(0);
    expect(failures).toEqual([]);
  });
});
