import { beforeEach, describe, expect, it } from 'vitest';
import { createInMemoryDocumentDatabase } from '@kb-labs/sdk/testing';
import type { IDocumentDatabase } from '@kb-labs/core-platform/adapters';
import { ApiTokensStore } from '../api-tokens-store.js';

describe('ApiTokensStore', () => {
  let docs: IDocumentDatabase;
  let store: ApiTokensStore;
  let now: number;

  beforeEach(() => {
    docs = createInMemoryDocumentDatabase();
    now = 1_000;
    store = new ApiTokensStore(docs, () => now);
  });

  it('stores a hash, returns the secret once, lists only metadata, and expires tokens', async () => {
    const created = await store.create({ userId: 'u1', tenantId: 't1', name: 'CI', ttlMs: 5_000 });
    expect(created.token).toMatch(/^kbl_pat_/);
    expect(created.record).toMatchObject({ userId: 'u1', tenantId: 't1', name: 'CI', issuedAt: 1_000, expiresAt: 6_000 });
    expect(await store.findActive(created.token)).toMatchObject(created.record);
    expect(await store.listByUser('u1')).toMatchObject([created.record]);
    now = 6_000;
    expect(await store.findActive(created.token)).toBeNull();
  });

  it('scopes revocation to the owner and immediately invalidates the token', async () => {
    const created = await store.create({ userId: 'u1', tenantId: 't1', name: 'CI', ttlMs: 5_000 });
    expect(await store.revokeByUser(created.record.tokenId, 'u2')).toBe(false);
    expect(await store.findActive(created.token)).not.toBeNull();
    expect(await store.revokeByUser(created.record.tokenId, 'u1')).toBe(true);
    expect(await store.findActive(created.token)).toBeNull();
  });
});
