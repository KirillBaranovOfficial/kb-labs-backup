import { describe, it } from 'vitest';
import { assertConditionalSingleDocumentUpdate } from '../../../adapters/contract/document-database.contract.js';
import { createInMemoryDocumentDatabase } from '../document-database.js';

describe('IDocumentDatabase conditional update contract — in-memory', () => {
  it('matches and updates one document atomically', async () => {
    const db = createInMemoryDocumentDatabase();
    try {
      await assertConditionalSingleDocumentUpdate(db);
    } finally {
      await db.close();
    }
  });
});
