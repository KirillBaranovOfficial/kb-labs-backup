/** Throwing fallback used when no local log buffer or persistence is configured. */

import type {
  ILogReader,
  LogCapabilities,
  LogQueryOptions,
  LogQueryResult,
  LogSearchOptions,
  LogSearchResult,
  LogStats,
} from '../../adapters/log-reader.js';
import type { LogQuery, LogRecord } from '../../adapters/logger.js';
import { AdapterUnavailableError } from '../../errors.js';

export class UnavailableLogReader implements ILogReader {
  private unavailable(): never {
    throw new AdapterUnavailableError('logs');
  }

  async query(_filters: LogQuery, _options?: LogQueryOptions): Promise<LogQueryResult> {
    return this.unavailable();
  }

  async getById(_id: string): Promise<LogRecord | null> {
    return this.unavailable();
  }

  async search(_searchText: string, _options?: LogSearchOptions): Promise<LogSearchResult> {
    return this.unavailable();
  }

  subscribe(_callback: (log: LogRecord) => void, _filters?: LogQuery): () => void {
    return this.unavailable();
  }

  async getStats(): Promise<LogStats> {
    return this.unavailable();
  }

  getCapabilities(): LogCapabilities {
    return { hasBuffer: false, hasPersistence: false, hasStreaming: false, hasSearch: false };
  }
}
