import { defineCommand } from '@kb-labs/sdk';

export default defineCommand({
  id: 'case-fixture:complete',
  description: 'Call platform.llm.complete and return its content',
  handler: {
    async execute(ctx, input) {
      const marker = input.flags.marker;
      if (!marker) throw new Error('journey llm complete requires --marker');
      const response = await ctx.platform.llm.complete(`Return the journey marker: ${marker}`, {
        model: 'journey-mock-model',
        execution: { cache: { mode: 'bypass' } },
      });
      return {
        ok: true,
        result: {
          marker,
          content: response.content,
          model: response.model,
          stopReason: response.usage?.providerUsage?.stopReason ?? null,
        },
      };
    },
  },
});
