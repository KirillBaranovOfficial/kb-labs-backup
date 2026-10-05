import { defineHandler, type PluginContextV3 } from '@kb-labs/sdk';
import { readdir } from 'node:fs/promises';
import path from 'node:path';
import { loadArchitecture, resolveArchitectureRoot, resolveSourceRoot } from '@kb-labs/viewer-core';

export default defineHandler({
  async execute(ctx: PluginContextV3) {
    const sources = [{ id: 'workspace', title: 'docs/architecture' }];
    const packageRoot = path.join(ctx.cwd, '.kb', 'viewer', 'sources');
    let folders: string[] = [];
    try { folders = await readdir(packageRoot); } catch { /* no imported sources yet */ }
    for (const id of folders.filter((name) => /^pkg-[a-z0-9-]{8,80}$/.test(name))) {
      try {
        const root = await resolveSourceRoot(ctx.cwd, id);
        const index = await loadArchitecture(root);
        sources.push({ id, title: index.title });
      } catch { /* ignore invalid or incomplete packages */ }
    }
    await resolveArchitectureRoot(ctx.cwd);
    return sources;
  },
});
