import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { SqliteVectorStore } from './vector-store.js';
import { manifest } from './manifest.js';

describe('SQLite vector store', () => {
  const directories: string[] = [];
  const stores: SqliteVectorStore[] = [];

  afterEach(async () => {
    for (const store of stores.splice(0)) {
      store.close();
    }
    for (const directory of directories.splice(0)) {
      await rm(directory, { recursive: true, force: true });
    }
  });

  async function createStore(directory?: string): Promise<SqliteVectorStore> {
    const databaseDirectory = directory ?? await mkdtemp(join(tmpdir(), 'kb-sqlite-vectors-'));
    if (directory === undefined) {
      directories.push(databaseDirectory);
    }
    const store = new SqliteVectorStore({ filename: join(databaseDirectory, 'vectors.sqlite') });
    stores.push(store);
    return store;
  }

  // case: S-054.25
  it('persists records, original IDs, namespaces, metadata filters, and cosine rankings', async () => {
    const first = await createStore();
    await first.upsert([
      { id: 'doc-1', vector: [1, 0], metadata: { group: 'docs', rank: 2 } },
      { id: 'doc-2', vector: [0, 1], metadata: { group: 'other', rank: 1 } },
    ], 'project');

    expect(await first.count('project')).toBe(2);
    expect(await first.search([0.8, 0.2], 2, { field: 'group', operator: 'eq', value: 'docs' }, 'project'))
      .toEqual([{ id: 'doc-1', score: expect.closeTo(0.97014, 4), metadata: { group: 'docs', rank: 2 } }]);

    first.close();
    stores.splice(stores.indexOf(first), 1);
    const reopened = await createStore(directories[0]);
    expect(await reopened.get(['doc-1'], 'project')).toEqual([
      { id: 'doc-1', vector: [1, 0], metadata: { group: 'docs', rank: 2 } },
    ]);
    expect(await reopened.search([1, 0], 1, undefined, 'project')).toEqual([
      { id: 'doc-1', score: 1, metadata: { group: 'docs', rank: 2 } },
    ]);
  });

  // case: S-054.1
  it('registers the SQLite package as the vectorStore catalogue default', () => {
    expect(manifest.defaultFor?.vectorStore).toEqual(['vector-store']);
    expect(manifest.configSchema?.vectorFilename).toMatchObject({
      pathBase: 'platformData',
      default: 'vector-store/vectors.sqlite',
    });
  });
});
