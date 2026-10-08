import type { AdapterManifest } from '@kb-labs/sdk/adapters';

export const manifest: AdapterManifest = {
  schema: 'kb.adapter/1',
  manifestVersion: '1.0.0',
  id: 'notifier-router',
  name: 'Notifier Router',
  version: '1.0.0',
  description: 'Routes platform notifications to configured channels',
  type: 'core',
  implements: 'INotifier',
  requires: {
    adapters: ['eventBus'],
    platformServices: [
      { id: 'resourceBroker', alias: 'broker' },
      { id: 'notifierChannels', alias: 'channels' },
    ],
  },
  configSchema: {
    channels: { type: 'object' },
    routing: { type: 'array' },
  },
};
