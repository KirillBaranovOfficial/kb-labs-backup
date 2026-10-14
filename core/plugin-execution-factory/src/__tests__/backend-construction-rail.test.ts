import { readdirSync, readFileSync } from 'node:fs';
import { dirname, extname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, it } from 'vitest';

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const workspaceRoot = resolve(packageRoot, '../..');

function sourceFiles(root: string): string[] {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    if (entry.isDirectory() && ['.git', 'dist', 'node_modules', 'coverage'].includes(entry.name)) return [];
    const path = join(root, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return ['.ts', '.tsx', '.mts', '.cts'].includes(extname(entry.name)) ? [path] : [];
  });
}

it('keeps concrete execution backend construction inside the factory package', () => {
  const backendSources = sourceFiles(join(packageRoot, 'src'))
    .filter((file) => !/(?:\.test|\.spec)\.[cm]?[jt]sx?$/.test(file));
  const constructors = new Set(backendSources.flatMap((file) =>
    [...readFileSync(file, 'utf8').matchAll(/export\s+class\s+(\w+)\s+implements\s+(?:Raw)?ExecutionBackend/g)]
      .map((match) => match[1]!),
  ));
  expect(constructors.size).toBeGreaterThan(0);

  const violations: string[] = [];
  for (const file of sourceFiles(workspaceRoot)) {
    if (file.startsWith(`${packageRoot}/`) || /(?:\.test|\.spec)\.[cm]?[jt]sx?$/.test(file)) continue;
    const source = readFileSync(file, 'utf8');
    for (const match of source.matchAll(/import\s+([\s\S]*?)\s+from\s+['"][^'"]+['"]/g)) {
      const clause = match[1]!;
      for (const constructor of constructors) {
        if (new RegExp(`\\b${constructor}\\b`).test(clause)) {
          violations.push(`${relative(workspaceRoot, file)} imports ${constructor}`);
        }
      }
    }
  }
  expect(violations).toEqual([]);
});
