import { defineCommand } from '@kb-labs/sdk';

export default defineCommand({
  id: 'case-fixture:emit',
  description: 'Emit the S-191 analytics event through platform.analytics.track',
  handler: {
    async execute(ctx, input) {
      const marker = input.flags.marker;
      if (!marker) throw new Error('journey analytics emit requires --marker');
      await ctx.platform.analytics.track('case.analytics.persist', { source: 'case-fixture', marker });
      return { ok: true, result: { type: 'case.analytics.persist', marker } };
    },
  },
});
