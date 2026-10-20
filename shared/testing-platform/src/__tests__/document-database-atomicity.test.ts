import { describe, it } from 'vitest';
import { assertConditionalSingleDocumentUpdate } from '@kb-labs/core-platform/adapters/contract';
import { createInMemoryDocumentDatabase } from '../in-memory-document-database.js';

describe('IDocumentDatabase conditional update contract — shared test platform', () => {
  it('matches and updates one document atomically', async () => {
    const db = createInMemoryDocumentDatabase();
    try {
      await assertConditionalSingleDocumentUpdate(db);
    } finally {
      await db.close();
    }
  });
});
