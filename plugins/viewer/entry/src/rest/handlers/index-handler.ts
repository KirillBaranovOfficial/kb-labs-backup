import { defineHandler, type PluginContextV3, type RestInput } from '@kb-labs/sdk';
import { loadArchitecture, resolveSourceRoot, listFiles } from '@kb-labs/viewer-core';

export default defineHandler({
  async execute(ctx: PluginContextV3, input: RestInput<{ path?: string; source?: string }>) {
    const root = await resolveSourceRoot(ctx.cwd, input.query?.source);
    const architecture = await loadArchitecture(root);
    const files = await listFiles(root, input.query?.path ?? '');
    return { architecture, files };
  },
});
