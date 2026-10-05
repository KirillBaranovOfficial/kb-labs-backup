import { combinePermissions, kbPlatformPreset } from '@kb-labs/sdk';

const permissions = combinePermissions()
  .with(kbPlatformPreset)
  .withFs({ mode: 'readWrite', allow: ['docs/architecture/**', '.kb/viewer/sources/**'] })
  .withShell({ allow: ['soffice'] })
  .withQuotas({ timeoutMs: 120_000, memoryMb: 512 })
  .build();

export const manifest = {
  schema: 'kb.plugin/3',
  id: '@kb-labs/viewer',
  version: '0.1.0',
  display: {
    name: 'Документы и архитектура',
    description: 'Просмотр архитектуры, Markdown, PDF и Office-документов проекта',
    tags: ['studio', 'architecture', 'documents', 'viewer'],
  },
  platform: { requires: [], optional: ['cache', 'logger'] },
  rest: {
    basePath: '/v1/plugins/viewer',
    routes: [
      { method: 'GET', path: '/index', handler: './rest/handlers/index-handler.js#default' },
      { method: 'GET', path: '/file', handler: './rest/handlers/file-handler.js#default' },
      { method: 'GET', path: '/convert', handler: './rest/handlers/convert-handler.js#default' },
      { method: 'GET', path: '/export', handler: './rest/handlers/export-handler.js#default' },
      { method: 'GET', path: '/sources', handler: './rest/handlers/sources-handler.js#default' },
      { method: 'POST', path: '/import', handler: './rest/handlers/import-handler.js#default' },
    ],
  },
  studio: {
    version: 2 as const,
    remoteName: 'viewerPlugin',
    pages: [{ id: 'viewer.overview', title: 'Документы и архитектура', icon: 'FileSearchOutlined', route: '/p/viewer', entry: './ViewerPage', order: 35 }],
    menus: [{ id: 'viewer', label: 'Документы и архитектура', icon: 'FileSearchOutlined', target: 'viewer.overview', order: 35 }],
  },
  permissions,
};

export default manifest;
