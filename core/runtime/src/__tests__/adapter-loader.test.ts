/**
 * @module @kb-labs/core-runtime/__tests__/adapter-loader
 * Tests for adapter dependency resolution and loading.
 */

import { describe, it, expect, vi } from 'vitest';
import { AdapterLoader, DependencyGraph } from '../adapter-loader.js';
import type { AdapterManifest, ILogger } from '@kb-labs/core-platform';
import type { LoadedAdapterModule, AdapterConfig } from '../adapter-loader.js';

describe('DependencyGraph', () => {
  it('should perform topological sort with no dependencies', () => {
    const graph = new DependencyGraph();

    graph.addNode({
      name: 'logger',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'logger', name: 'Logger', version: '1.0.0', type: 'core', implements: 'ILogger', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'cache',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'cache', name: 'Cache', version: '1.0.0', type: 'core', implements: 'ICache', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    const sorted = graph.topologicalSort();
    expect(sorted).toHaveLength(2);
    expect(sorted.map((n) => n.name)).toContain('logger');
    expect(sorted.map((n) => n.name)).toContain('cache');
  });

  it('should sort with linear dependency chain', () => {
    const graph = new DependencyGraph();

    // logger -> logBuffer -> logPersistence
    graph.addNode({
      name: 'logger',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'logger', name: 'Logger', version: '1.0.0', type: 'core', implements: 'ILogger', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'logBuffer',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'logBuffer', name: 'Log Buffer', version: '1.0.0', type: 'extension', implements: 'ILogBuffer', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'logPersistence',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'logPersistence', name: 'Log Persistence', version: '1.0.0', type: 'extension', implements: 'ILogPersistence', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: ['db'],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'db',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'db', name: 'Database', version: '1.0.0', type: 'core', implements: 'ISQLDatabase', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    // Add edge: db -> logPersistence (db must load before logPersistence)
    graph.addEdge('db', 'logPersistence');

    const sorted = graph.topologicalSort();
    expect(sorted).toHaveLength(4);

    // db must come before logPersistence
    const dbIndex = sorted.findIndex((n) => n.name === 'db');
    const persistIndex = sorted.findIndex((n) => n.name === 'logPersistence');
    expect(dbIndex).toBeLessThan(persistIndex);
  });

  it('should detect circular dependency', () => {
    const graph = new DependencyGraph();

    graph.addNode({
      name: 'a',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'a', name: 'A', version: '1.0.0', type: 'core', implements: 'IA', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: ['b'],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'b',
      manifest: { schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'b', name: 'B', version: '1.0.0', type: 'core', implements: 'IB', networkRequired: false },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: ['a'],
      optionalDeps: [],
      inDegree: 0,
    });

    // Create cycle: a depends on b, b depends on a
    // a -> b (b must load before a)
    // b -> a (a must load before b)
    // This creates a cycle
    graph.addEdge('b', 'a'); // b needs to load before a
    graph.addEdge('a', 'b'); // a needs to load before b (cycle!)

    expect(() => graph.topologicalSort()).toThrow('Circular dependency');
  });
});

describe('AdapterLoader', () => {
  // case: S-211.6
  it('isolates an adapter factory failure and continues loading other slots when requested', async () => {
    const failedFactory = vi.fn(() => { throw new Error('bad adapter configuration'); });
    const healthyFactory = vi.fn(() => ({ ready: true }));
    const failures: Array<{ slot: string; packageName: string; message: string }> = [];
    const loader = new AdapterLoader();
    const loaded = await loader.loadAdapters({
      broken: { module: '@example/broken', config: {} },
      healthy: { module: '@example/healthy', config: {} },
    }, async (modulePath) => ({
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: modulePath.includes('broken') ? 'broken' : 'healthy',
        name: modulePath,
        version: '1.0.0',
        type: 'core',
        implements: 'ITest', networkRequired: false,
      },
      createAdapter: modulePath.includes('broken') ? failedFactory : healthyFactory,
    }), {
      onAdapterLoadFailure: (slot, packageName, error) => {
        failures.push({ slot, packageName, message: error instanceof Error ? error.message : String(error) });
      },
    });

    expect(failures).toEqual([{ slot: 'broken', packageName: '@example/broken', message: 'bad adapter configuration' }]);
    expect(loaded.has('broken')).toBe(false);
    expect(loaded.get('healthy')).toEqual({ ready: true });
  });

  it('provides the platform logger to adapter factories through shared dependencies', async () => {
    let logger: ILogger;
    logger = {
      info: vi.fn(), warn: vi.fn(), error: vi.fn(), fatal: vi.fn(), debug: vi.fn(), trace: vi.fn(),
      child: vi.fn(() => logger),
    };
    const createAdapter = vi.fn((_config, deps) => {
      expect(deps.logger).toBe(logger);
      return {};
    });
    const loader = new AdapterLoader();
    const configs = { cache: { module: '@example/cache', config: {} } };
    const loadModule = vi.fn(async (): Promise<LoadedAdapterModule> => ({
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'cache', name: 'Cache', version: '1.0.0', type: 'core', implements: 'ICache', networkRequired: false,
      },
      createAdapter,
    }));

    await loader.loadAdapters(configs, loadModule, { logger });

    expect(createAdapter).toHaveBeenCalledTimes(1);
  });

  it('injects declared platform services and external adapter dependencies through the generic loader', async () => {
    const loader = new AdapterLoader();
    const eventBus = { publish: vi.fn() };
    const broker = { register: vi.fn() };
    const channels = vi.fn();
    const createAdapter = vi.fn((_config: unknown, deps: Record<string, unknown>) => deps);
    const loadModule = vi.fn(async (): Promise<LoadedAdapterModule> => ({
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'notifier', name: 'Notifier', version: '1.0.0', type: 'core',
        implements: 'INotifier', networkRequired: false, requires: {
          adapters: ['eventBus'],
          platformServices: [
            { id: 'resourceBroker', alias: 'broker' },
            { id: 'notifierChannels', alias: 'channels' },
          ],
        },
      },
      createAdapter,
    }));

    const loaded = await loader.loadAdapters(
      { notifier: { module: '@kb-labs/notifier-router', config: {} } },
      loadModule,
      {
        adapters: new Map([['eventBus', eventBus]]),
        platformServices: {
          resourceBroker: () => broker,
          notifierChannels: async () => channels,
        },
      },
    );

    expect(loaded.get('notifier')).toEqual({ eventBus, broker, channels });
    expect((loaded.get('notifier') as Record<string, unknown>).channels).toBe(channels);
  });

  it('should build dependency graph from configs', async () => {
    const loader = new AdapterLoader();

    const configs: Record<string, AdapterConfig> = {
      logger: { module: '@kb-labs/adapters-pino', config: { level: 'info' } },
      db: { module: '@kb-labs/adapters-sqlite', config: {} },
    };

    const loadModule = vi.fn(async (modulePath: string) => {
      if (modulePath === '@kb-labs/adapters-pino') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'pino-logger',
            name: 'Pino Logger',
            version: '1.0.0',
            type: 'core',
            implements: 'ILogger', networkRequired: false,
          } as AdapterManifest,
          createAdapter: vi.fn(),
        };
      }
      if (modulePath === '@kb-labs/adapters-sqlite') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'sqlite-db',
            name: 'SQLite Database',
            version: '1.0.0',
            type: 'core',
            implements: 'ISQLDatabase', networkRequired: false,
          } as AdapterManifest,
          createAdapter: vi.fn(),
        };
      }
      throw new Error(`Unknown module: ${modulePath}`);
    });

    const graph = await loader.buildDependencyGraph(configs, loadModule);

    expect(graph.getNode('logger')).toBeDefined();
    expect(graph.getNode('db')).toBeDefined();
    expect(loadModule).toHaveBeenCalledTimes(2);
  });

  it('should throw error for missing required dependency', async () => {
    const loader = new AdapterLoader();

    const configs: Record<string, AdapterConfig> = {
      logPersistence: { module: '@kb-labs/adapters-log-document', config: {} },
      // Missing 'documentDatabase' adapter
    };

    const loadModule = vi.fn(async () => {
      return {
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'log-persistence',
          name: 'Log Persistence',
          version: '1.0.0',
          type: 'extension',
          implements: 'ILogPersistence', networkRequired: false,
          requires: { adapters: ['documentDatabase'] },
        } as AdapterManifest,
        createAdapter: vi.fn(),
      };
    });

    await expect(
      loader.buildDependencyGraph(configs, loadModule)
    ).rejects.toThrow('requires adapter "documentDatabase"');
  });

  it('should explain runtime token vs manifest id mismatch', async () => {
    const loader = new AdapterLoader();

    const configs: Record<string, AdapterConfig> = {
      cache: { module: '@kb-labs/adapters-redis', config: {} },
      eventBus: { module: '@kb-labs/adapters-eventbus-cache', config: {} },
    };

    const loadModule = vi.fn(async (modulePath: string) => {
      if (modulePath === '@kb-labs/adapters-redis') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'redis-cache',
            name: 'Redis Cache',
            version: '1.0.0',
            type: 'core',
            implements: 'ICache', networkRequired: false,
          } as AdapterManifest,
          createAdapter: vi.fn(),
        };
      }

      if (modulePath === '@kb-labs/adapters-eventbus-cache') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'eventbus-cache',
            name: 'EventBus Cache',
            version: '1.0.0',
            type: 'core',
            implements: 'IEventBus', networkRequired: false,
            // Intentionally wrong: uses manifest.id instead of runtime token "cache"
            requires: { adapters: ['redis-cache'] },
          } as AdapterManifest,
          createAdapter: vi.fn(),
        };
      }

      throw new Error(`Unknown module: ${modulePath}`);
    });

    await expect(
      loader.buildDependencyGraph(configs, loadModule)
    ).rejects.toThrow('runtime adapter tokens');
  });

  it('should handle optional dependencies gracefully', async () => {
    const loader = new AdapterLoader();

    const configs: Record<string, AdapterConfig> = {
      logger: { module: '@kb-labs/adapters-pino', config: {} },
      // Missing 'analytics' (optional dependency)
    };

    const loadModule = vi.fn(async () => {
      return {
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'pino-logger',
          name: 'Pino Logger',
          version: '1.0.0',
          type: 'core',
          implements: 'ILogger', networkRequired: false,
          optional: { adapters: ['analytics'] },
        } as AdapterManifest,
        createAdapter: vi.fn(),
      };
    });

    const graph = await loader.buildDependencyGraph(configs, loadModule);
    expect(graph.getNode('logger')).toBeDefined();
    // Should not throw error for missing optional dependency
  });

  it('should load adapters in dependency order', async () => {
    const loader = new AdapterLoader();

    const configs: Record<string, AdapterConfig> = {
      documentDatabase: { module: '@kb-labs/adapters-sqlite', config: {} },
      logPersistence: { module: '@kb-labs/adapters-log-document', config: {} },
    };

    const mockDB = { find: vi.fn(), insertMany: vi.fn(), ensureCollection: vi.fn() };
    const mockPersistence = { write: vi.fn() };

    const loadModule = vi.fn(async (modulePath: string) => {
      if (modulePath === '@kb-labs/adapters-sqlite') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'sqlite-stores',
            name: 'SQLite',
            version: '1.0.0',
            type: 'core',
            implements: 'IDocumentDatabase', networkRequired: false,
          } as AdapterManifest,
          createAdapter: vi.fn(() => mockDB),
        };
      }
      if (modulePath === '@kb-labs/adapters-log-document') {
        return {
          manifest: {
            schema: 'kb.adapter/1', manifestVersion: '1.0.0',
            id: 'log-document',
            name: 'Log Persistence',
            version: '1.0.0',
            type: 'extension',
            implements: 'ILogPersistence', networkRequired: false,
            requires: { adapters: [{ id: 'documentDatabase', alias: 'documentDatabase' }] },
          } as AdapterManifest,
          createAdapter: vi.fn((config, deps) => {
            expect(deps.documentDatabase).toBe(mockDB);
            return mockPersistence;
          }),
        };
      }
      throw new Error(`Unknown module: ${modulePath}`);
    });

    const adapters = await loader.loadAdapters(configs, loadModule);

    expect(adapters.get('documentDatabase')).toBe(mockDB);
    expect(adapters.get('logPersistence')).toBe(mockPersistence);
  });

  it('should connect extensions to core adapters', () => {
    const loader = new AdapterLoader();

    // Mock logger with onLog hook
    const mockLogger = {
      onLog: vi.fn(),
    };

    // Mock ring buffer with append method
    const mockRingBuffer = {
      append: vi.fn(),
    };

    // Create graph
    const graph = new DependencyGraph();

    graph.addNode({
      name: 'logger',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'logger',
        name: 'Logger',
        version: '1.0.0',
        type: 'core',
        implements: 'ILogger', networkRequired: false,
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'logRingBuffer',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'logRingBuffer',
        name: 'Log Ring Buffer',
        version: '1.0.0',
        type: 'extension',
        implements: 'ILogRingBuffer', networkRequired: false,
        extends: {
          adapter: 'logger',
          hook: 'onLog',
          method: 'append',
          priority: 10,
        },
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    const adapters = new Map<string, unknown>();
    adapters.set('logger', mockLogger);
    adapters.set('logRingBuffer', mockRingBuffer);

    loader.connectExtensions(adapters, graph);

    expect(mockLogger.onLog).toHaveBeenCalledTimes(1);
    expect(mockLogger.onLog).toHaveBeenCalledWith(expect.any(Function));
  });

  it('should sort extensions by priority (higher first)', () => {
    const loader = new AdapterLoader();

    const mockLogger = { onLog: vi.fn() };
    const ext1 = { method: vi.fn() };
    const ext2 = { method: vi.fn() };
    const ext3 = { method: vi.fn() };

    const graph = new DependencyGraph();

    graph.addNode({
      name: 'logger',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'logger',
        name: 'Logger',
        version: '1.0.0',
        type: 'core',
        implements: 'ILogger', networkRequired: false,
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    // Priority: 10, 5, default (0)
    graph.addNode({
      name: 'ext1',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'ext1',
        name: 'Extension 1',
        version: '1.0.0',
        type: 'extension',
        implements: 'IExt', networkRequired: false,
        extends: { adapter: 'logger', hook: 'onLog', method: 'method', priority: 10 },
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'ext2',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'ext2',
        name: 'Extension 2',
        version: '1.0.0',
        type: 'extension',
        implements: 'IExt', networkRequired: false,
        extends: { adapter: 'logger', hook: 'onLog', method: 'method', priority: 5 },
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    graph.addNode({
      name: 'ext3',
      manifest: {
        schema: 'kb.adapter/1', manifestVersion: '1.0.0',
        id: 'ext3',
        name: 'Extension 3',
        version: '1.0.0',
        type: 'extension',
        implements: 'IExt', networkRequired: false,
        extends: { adapter: 'logger', hook: 'onLog', method: 'method' }, // priority: 0 (default)
      },
      module: {} as LoadedAdapterModule,
      config: {},
      requiredDeps: [],
      optionalDeps: [],
      inDegree: 0,
    });

    const adapters = new Map<string, unknown>();
    adapters.set('logger', mockLogger);
    adapters.set('ext1', ext1);
    adapters.set('ext2', ext2);
    adapters.set('ext3', ext3);

    loader.connectExtensions(adapters, graph);

    // Should be called 3 times in priority order: ext1 (10), ext2 (5), ext3 (0)
    expect(mockLogger.onLog).toHaveBeenCalledTimes(3);
  });

  describe('validateExtensionConnection (warning paths)', () => {
    it('should warn and skip when target adapter is not in adapters map', () => {
      const loader = new AdapterLoader();
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      const graph = new DependencyGraph();
      graph.addNode({
        name: 'ext1',
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'ext1',
          name: 'Extension 1',
          version: '1.0.0',
          type: 'extension',
          implements: 'IExt', networkRequired: false,
          extends: { adapter: 'missing-adapter', hook: 'onLog', method: 'handle' },
        },
        module: {} as LoadedAdapterModule,
        config: {},
        requiredDeps: [],
        optionalDeps: [],
        inDegree: 0,
      });

      const ext = { handle: vi.fn() };
      const adapters = new Map<string, unknown>();
      adapters.set('ext1', ext);
      // 'missing-adapter' intentionally absent

      loader.connectExtensions(adapters, graph);

      expect(warnSpy).toHaveBeenCalledWith(
        expect.stringContaining('target adapter "missing-adapter" not found')
      );
      expect(ext.handle).not.toHaveBeenCalled();

      warnSpy.mockRestore();
    });

    it('should warn and skip when target adapter has no hook method', () => {
      const loader = new AdapterLoader();
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      const graph = new DependencyGraph();
      graph.addNode({
        name: 'ext1',
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'ext1',
          name: 'Extension 1',
          version: '1.0.0',
          type: 'extension',
          implements: 'IExt', networkRequired: false,
          extends: { adapter: 'logger', hook: 'nonExistentHook', method: 'handle' },
        },
        module: {} as LoadedAdapterModule,
        config: {},
        requiredDeps: [],
        optionalDeps: [],
        inDegree: 0,
      });

      const logger = { onLog: vi.fn() }; // has onLog, but NOT nonExistentHook
      const ext = { handle: vi.fn() };
      const adapters = new Map<string, unknown>();
      adapters.set('logger', logger);
      adapters.set('ext1', ext);

      loader.connectExtensions(adapters, graph);

      expect(warnSpy).toHaveBeenCalledWith(
        expect.stringContaining('target "logger" has no method "nonExistentHook"')
      );
      expect(ext.handle).not.toHaveBeenCalled();

      warnSpy.mockRestore();
    });

    it('should warn and skip when extension instance has no method', () => {
      const loader = new AdapterLoader();
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      const graph = new DependencyGraph();
      graph.addNode({
        name: 'ext1',
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'ext1',
          name: 'Extension 1',
          version: '1.0.0',
          type: 'extension',
          implements: 'IExt', networkRequired: false,
          extends: { adapter: 'logger', hook: 'onLog', method: 'nonExistentMethod' },
        },
        module: {} as LoadedAdapterModule,
        config: {},
        requiredDeps: [],
        optionalDeps: [],
        inDegree: 0,
      });

      const logger = { onLog: vi.fn() };
      const ext = { handle: vi.fn() }; // has handle, but NOT nonExistentMethod
      const adapters = new Map<string, unknown>();
      adapters.set('logger', logger);
      adapters.set('ext1', ext);

      loader.connectExtensions(adapters, graph);

      expect(warnSpy).toHaveBeenCalledWith(
        expect.stringContaining('extension has no method "nonExistentMethod"')
      );
      expect(logger.onLog).not.toHaveBeenCalled();

      warnSpy.mockRestore();
    });

    it('should warn for invalid extension but still connect valid ones', () => {
      const loader = new AdapterLoader();
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      const graph = new DependencyGraph();

      // Invalid: target missing
      graph.addNode({
        name: 'bad-ext',
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'bad-ext',
          name: 'Bad Extension',
          version: '1.0.0',
          type: 'extension',
          implements: 'IExt', networkRequired: false,
          extends: { adapter: 'missing', hook: 'onLog', method: 'handle' },
        },
        module: {} as LoadedAdapterModule,
        config: {},
        requiredDeps: [],
        optionalDeps: [],
        inDegree: 0,
      });

      // Valid extension
      graph.addNode({
        name: 'good-ext',
        manifest: {
          schema: 'kb.adapter/1', manifestVersion: '1.0.0',
          id: 'good-ext',
          name: 'Good Extension',
          version: '1.0.0',
          type: 'extension',
          implements: 'IExt', networkRequired: false,
          extends: { adapter: 'logger', hook: 'onLog', method: 'handle' },
        },
        module: {} as LoadedAdapterModule,
        config: {},
        requiredDeps: [],
        optionalDeps: [],
        inDegree: 0,
      });

      const logger = { onLog: vi.fn() };
      const goodExt = { handle: vi.fn() };
      const badExt = { handle: vi.fn() };
      const adapters = new Map<string, unknown>();
      adapters.set('logger', logger);
      adapters.set('good-ext', goodExt);
      adapters.set('bad-ext', badExt);

      loader.connectExtensions(adapters, graph);

      expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('"bad-ext"'));
      expect(logger.onLog).toHaveBeenCalledTimes(1); // good-ext connected
      expect(badExt.handle).not.toHaveBeenCalled();

      warnSpy.mockRestore();
    });
  });
});
