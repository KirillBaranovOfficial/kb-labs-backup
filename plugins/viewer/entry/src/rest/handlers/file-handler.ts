import { defineHandler, type PluginContextV3, type RestInput } from '@kb-labs/sdk';
import { readFile, stat } from 'node:fs/promises';
import { resolveSourceRoot, resolveInside, readTextFile } from '@kb-labs/viewer-core';

export default defineHandler({
  async execute(ctx: PluginContextV3, input: RestInput<{ path: string; source?: string }>) {
    const relative = input.query?.path;
    if (!relative) throw new Error('The path query parameter is required');
    const root = await resolveSourceRoot(ctx.cwd, input.query?.source);
    const absolute = await resolveInside(root, relative);
    const info = await stat(absolute);
    if (!info.isFile() || info.size > 40 * 1024 * 1024) throw new Error('File is too large or unavailable');
    const extension = relative.split('.').pop()?.toLowerCase();
    if (extension === 'pdf') {
      return { path: relative, kind: 'pdf', data: (await readFile(absolute)).toString('base64') };
    }
    if (['doc', 'docx'].includes(extension ?? '')) {
      return { path: relative, kind: 'office' };
    }
    return { path: relative, ...(await readTextFile(root, relative)) };
  },
});
