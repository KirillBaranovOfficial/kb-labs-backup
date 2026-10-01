/** Throwing adapter used when no vector-store provider is configured. */

import { AdapterUnavailableError } from '../../errors.js';
import type { IVectorStore, VectorFilter, VectorRecord, VectorSearchResult } from '../../adapters/vector-store.js';

export class UnavailableVectorStore implements IVectorStore {
  private unavailable(): never {
    throw new AdapterUnavailableError('vectorStore');
  }

  async search(_query: number[], _limit: number, _filter?: VectorFilter, _namespace?: string): Promise<VectorSearchResult[]> {
    return this.unavailable();
  }

  async upsert(_vectors: VectorRecord[], _namespace?: string): Promise<void> {
    this.unavailable();
  }

  async delete(_ids: string[], _namespace?: string): Promise<void> {
    this.unavailable();
  }

  async count(_namespace?: string): Promise<number> {
    return this.unavailable();
  }

  async get(_ids: string[], _namespace?: string): Promise<VectorRecord[]> {
    return this.unavailable();
  }

  async query(_filter: VectorFilter, _namespace?: string): Promise<VectorRecord[]> {
    return this.unavailable();
  }
}
