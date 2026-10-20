export const manifest = {
  schema: 'kb.plugin/3',
  id: '@kb-labs/e2e-analytics-emitter',
  version: '1.0.0',
  display: {
    name: 'Journey Analytics Fixture',
    description: 'Deterministic analytics and LLM provider journey fixture',
  },
  permissions: { platform: { analytics: true, llm: true } },
  platform: { requires: ['analytics', 'llm'] },
  cli: {
    groupMeta: [{ path: 'journey', describe: 'Journey fixture commands' }],
    commands: [
      {
        path: 'journey analytics emit',
        describe: 'Emit a uniquely marked analytics event through the platform SDK',
        operationType: 'execute',
        handler: './commands/emit.js#default',
        flags: [{ name: 'marker', type: 'string', description: 'Unique event marker', required: true }],
      },
      {
        path: 'journey llm complete',
        describe: 'Exercise the configured platform LLM adapter',
        operationType: 'execute',
        handler: './commands/complete.js#default',
        flags: [{ name: 'marker', type: 'string', description: 'Unique request marker', required: true }],
      },
    ],
  },
};
