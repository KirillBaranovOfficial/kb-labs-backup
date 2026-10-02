import { afterEach, describe, expect, it } from 'vitest';
import type { IEntityRegistry, RegistrySnapshot } from '@kb-labs/core-registry';
import { restApiConfigSchema } from '@kb-labs/rest-api-core';
import { createServer } from '../server';

const snapshot: RegistrySnapshot = {
  schema: 'kb.registry/1',
  rev: 1,
  version: 'test',
  generatedAt: new Date(0).toISOString(),
  partial: false,
  stale: false,
  source: { cwd: process.cwd(), platformVersion: 'test' },
  plugins: [],
  manifests: [],
  ts: 0,
};

const registry = {
  initialize: async () => undefined,
  listPlugins: () => [],
  getManifest: () => null,
  getOpenAPISpec: () => null,
  getStudioRegistry: () => ({ version: 'test', generated: new Date(0).toISOString(), plugins: [] }),
  queryEntities: () => [],
  getEntity: () => null,
  getEntityKinds: () => [],
  snapshot: () => snapshot,
  refresh: async () => undefined,
  onChange: () => () => undefined,
  getSystemHealth: async () => ({ status: 'healthy', ts: new Date(0).toISOString() }),
  getDiagnostics: () => ({ events: [], summary: { errors: 0, warnings: 0, totalPlugins: 0, loadedPlugins: 0, failedPlugins: 0 }, byPlugin: {} }),
  enablePlugin: async () => undefined,
  disablePlugin: async () => undefined,
  dispose: async () => undefined,
} as unknown as IEntityRegistry;

describe('REST production route declarations', () => {
  const apps: Array<Awaited<ReturnType<typeof createServer>>> = [];

  afterEach(async () => {
    await Promise.all(apps.splice(0).map((app) => app.close()));
  });

  // case: S-203.1, S-203.2, S-203.3, S-203.6
  it('discovers routes from the production server registry and requires a schema or explicit raw declaration', async () => {
    const routes: Array<{ method: string | string[]; url: string; schema?: unknown; config?: unknown }> = [];
    const app = await createServer(restApiConfigSchema.parse({ port: 5050 }), process.cwd(), registry, (route) => routes.push(route));
    apps.push(app);
    await app.ready();

    expect(routes.length).toBeGreaterThan(0);
    const missing = routes.filter((route) => {
      const config = route.config as { ux?: string } | undefined;
      return route.schema === undefined && config?.ux !== 'raw';
    });
    expect(missing.map(({ method, url }) => `${Array.isArray(method) ? method.join(',') : method} ${url}`)).toEqual([]);
  });
});
