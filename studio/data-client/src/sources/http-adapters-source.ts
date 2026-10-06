/**
 * @module @kb-labs/studio-data-client/sources/http-adapters-source
 * HTTP implementation of AdaptersDataSource
 */

import type { HttpClient } from '../client/http-client';
import type { AdaptersDataSource, DateRangeOptions, DailyStats } from './adapters-source';
import type {
  LLMUsageStats,
  EmbeddingsUsageStats,
} from '../contracts/adapters';

/**
 * HTTP implementation of AdaptersDataSource
 * Calls REST API /v1/adapters/* endpoints
 */
export class HttpAdaptersSource implements AdaptersDataSource {
  constructor(private readonly client: HttpClient, private readonly projectId?: string) {}

  private path(path: string): string {
    if (!this.projectId) {throw new Error('Select a project to view project adapter usage.');}
    return `/projects/${encodeURIComponent(this.projectId)}${path}`;
  }

  /**
   * Build query string from date range options
   */
  private buildQueryString(options?: DateRangeOptions): string {
    const params = new URLSearchParams();

    if (options?.from) {params.append('from', options.from);}
    if (options?.to) {params.append('to', options.to);}

    // Legacy model filter
    if (options?.models) {
      const modelsArray = Array.isArray(options.models) ? options.models : [options.models];
      if (modelsArray.length > 0) {params.append('models', modelsArray.join(','));}
    }

    // StatsQuery fields
    if (options?.groupBy) {params.append('groupBy', options.groupBy);}
    if (options?.breakdownBy) {params.append('breakdownBy', options.breakdownBy);}
    if (options?.metrics && options.metrics.length > 0) {params.append('metrics', options.metrics.join(','));}

    const qs = params.toString();
    return qs ? `?${qs}` : '';
  }

  async getLLMUsage(options?: DateRangeOptions): Promise<LLMUsageStats> {
    const query = this.buildQueryString(options);
    return this.client.fetch<LLMUsageStats>(this.path(`/adapters/llm/usage${query}`));
  }

  async getEmbeddingsUsage(options?: DateRangeOptions): Promise<EmbeddingsUsageStats> {
    const query = this.buildQueryString(options);
    return this.client.fetch<EmbeddingsUsageStats>(this.path(`/adapters/embeddings/usage${query}`));
  }

  async getLLMDailyStats(options?: DateRangeOptions): Promise<DailyStats[]> {
    const query = this.buildQueryString(options);
    return this.client.fetch<DailyStats[]>(this.path(`/adapters/llm/daily-stats${query}`));
  }

  async getEmbeddingsDailyStats(options?: DateRangeOptions): Promise<DailyStats[]> {
    const query = this.buildQueryString(options);
    return this.client.fetch<DailyStats[]>(this.path(`/adapters/embeddings/daily-stats${query}`));
  }

}
