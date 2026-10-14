/**
 * @module @kb-labs/marketplace-cli/manifest
 * ManifestV3 for marketplace CLI plugin.
 * Declares commands with subgroups for nested routing.
 */

import { combinePermissions, kbPlatformPreset } from '@kb-labs/sdk';

const pluginPermissions = combinePermissions()
  .with(kbPlatformPreset)
  .withEnv(['KB_MARKETPLACE_URL', 'KB_GATEWAY_URL', 'NODE_ENV'])
  .build();

const scopeFlag = {
  name: 'scope',
  type: 'string' as const,
  choices: ['platform', 'project'] as const,
  description: 'Marketplace scope to operate on',
};

export const manifest = {
  schema: 'kb.plugin/3' as const,
  id: '@kb-labs/marketplace',
  version: '0.1.0',
  display: {
    name: 'Marketplace',
    description: 'Unified marketplace for plugins and adapters',
    tags: ['marketplace', 'plugins', 'adapters'],
  },
  cli: {
    groupMeta: [
      { path: 'marketplace', describe: 'Marketplace management commands' },
      { path: 'marketplace plugins', describe: 'Plugin management' },
    ],
    commands: [
      // Top-level: kb marketplace <cmd>
      { path: 'marketplace install', operationType: 'mutate' as const, describe: 'Install package(s)', flags: [
        { name: 'dev', type: 'boolean' as const, description: 'Install development dependencies' },
        { name: 'token', type: 'string' as const, description: 'Marketplace access token', secret: true },
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without installing' },
        scopeFlag,
      ], handler: './commands/install.js#default' },
      { path: 'marketplace uninstall', operationType: 'mutate' as const, describe: 'Uninstall package(s)', flags: [
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without uninstalling' }, scopeFlag,
      ], handler: './commands/uninstall.js#default' },
      { path: 'marketplace update', operationType: 'mutate' as const, describe: 'Update package(s)', flags: [
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without updating' }, scopeFlag,
      ], handler: './commands/update.js#default' },
      { path: 'marketplace sync', operationType: 'mutate' as const, describe: 'Sync workspace to lock', flags: [
        { name: 'auto-enable', type: 'boolean' as const, description: 'Enable discovered packages automatically' },
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without updating the lock' }, scopeFlag,
      ], handler: './commands/sync.js#default' },
      // Subgroup: kb marketplace plugins <cmd>
      { path: 'marketplace plugins add', operationType: 'mutate' as const, describe: 'Register a local project plugin', flags: [
        { name: 'link', type: 'string' as const, description: 'Project-relative path to the local plugin package' },
        { name: 'json', type: 'boolean' as const, description: 'Output JSON for automation' },
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without registering' },
        scopeFlag,
      ], examples: ['kb marketplace plugins add --link .kb/plugins/my-plugin/packages/my-plugin-entry'], handler: './commands/plugins/add.js#default' },
      { path: 'marketplace plugins remove', operationType: 'mutate' as const, describe: 'Remove a local project plugin registration', flags: [
        { name: 'json', type: 'boolean' as const, description: 'Output JSON for automation' },
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without removing' },
        scopeFlag,
      ], examples: ['kb marketplace plugins remove @kb-labs/my-plugin'], handler: './commands/plugins/remove.js#default' },
      { path: 'marketplace plugins list', operationType: 'read' as const, describe: 'List installed plugins', flags: [scopeFlag], handler: './commands/plugins/list.js#default' },
      { path: 'marketplace plugins enable', operationType: 'mutate' as const, describe: 'Enable a plugin', flags: [
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without enabling' }, scopeFlag,
      ], handler: './commands/plugins/enable.js#default' },
      { path: 'marketplace plugins disable', operationType: 'mutate' as const, describe: 'Disable a plugin', flags: [
        { name: 'dry-run', type: 'boolean' as const, description: 'Preview without disabling' }, scopeFlag,
      ], handler: './commands/plugins/disable.js#default' },
      { path: 'marketplace plugins doctor', operationType: 'analyze' as const, describe: 'Diagnose issues', flags: [scopeFlag], handler: './commands/plugins/doctor.js#default' },
    ],
  },
  permissions: pluginPermissions,
};
