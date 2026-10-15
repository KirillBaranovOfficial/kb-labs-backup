import { PluginError } from '@kb-labs/plugin-contracts';

export default {
  async execute() {
    const error = new PluginError('typed plugin failure', 'KB_PLUGIN_PERMISSION_DENIED');
    error.stack = 'PluginError: typed plugin failure\n    at registeredHandler (bundled-plugin.js:31:5)';
    throw error;
  },
};
