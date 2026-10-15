import { AdapterUnavailableError } from '@kb-labs/core-platform';

export default {
  async execute() {
    const error = new AdapterUnavailableError('llm', 'not-configured', 'fixture adapter unavailable');
    error.stack = 'AdapterUnavailableError: fixture adapter unavailable\n    at registeredHandler (adapter-plugin.js:19:7)';
    throw error;
  },
};
