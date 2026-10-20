import { defineConfig } from 'tsup';
import { readFileSync } from 'node:fs';
import nodePreset from '@kb-labs/devkit/tsup/node';

const presetExternal = nodePreset.external || [];
const packageMetadata = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')) as { version: string };
if (!packageMetadata.version.trim()) {
  throw new Error('CLI package.json must define a non-empty version before the binary can be built.');
}

export default defineConfig({
  ...nodePreset,
  entry: {
    index: "src/index.ts",
    bin: "src/bin.ts",
    "runtime/execution-error": "src/runtime/execution-error.ts",
  },
  tsconfig: "tsconfig.build.json", // Используем конфиг без paths, чтобы избежать бандлинга workspace пакетов
  clean: false,
  dts: {
    resolve: false,
  },
  // ✅ CRITICAL: Extend preset external with explicit core packages
  // This prevents tsup from bundling core-runtime, which would create duplicate
  // platform singletons (one in CLI process, one in sandbox worker)
  external: [
    ...Array.isArray(presetExternal) ? presetExternal : [],
    // Explicitly add core-runtime and core-sandbox (may already be in preset, but ensure it)
    '@kb-labs/core-runtime',
    '@kb-labs/core-sandbox',
  ],
  define: {
    __KB_CLI_VERSION__: JSON.stringify(packageMetadata.version),
  },
  // nodePreset уже включает все workspace пакеты через tsup.external.json
  // banner не нужен - shebang уже есть в src/bin.ts
});
