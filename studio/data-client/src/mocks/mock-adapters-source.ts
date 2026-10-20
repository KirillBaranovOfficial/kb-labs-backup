/**
 * @module @kb-labs/studio-data-client/mocks/mock-adapters-source
 * Mock implementation of AdaptersDataSource
 */

import type { AdaptersDataSource, DateRangeOptions, DailyStats } from '../sources/adapters-source';
import type {
  LLMUsageStats,
  EmbeddingsUsageStats,
} from '../contracts/adapters';

/**
 * Mock adapters data source for testing
 */
export class MockAdaptersSource implements AdaptersDataSource {
  async getLLMUsage(_options?: DateRangeOptions): Promise<LLMUsageStats> {
    return {
      totalRequests: 1247,
      totalTokens: 3_456_789,
      totalCost: 12.45,
      totalCacheReadTokens: 0,
      totalBillableTokens: 3_456_789,
      totalCacheSavingsUsd: 0,
      byModel: {
        'gpt-4': {
          requests: 234,
          promptTokens: 45_678,
          completionTokens: 23_456,
          totalTokens: 69_134,
          cost: 4.15,
          costPer1KTokens: 0.06,
          tokensPerRequest: 295,
          errorRate: 0.5,
          avgDurationMs: 2345,
          cacheReadTokens: 0,
          billableTokens: 69_134,
          cacheSavingsUsd: 0,
        },
        'gpt-3.5-turbo': {
          requests: 789,
          promptTokens: 234_567,
          completionTokens: 123_456,
          totalTokens: 358_023,
          cost: 0.54,
          costPer1KTokens: 0.0015,
          tokensPerRequest: 454,
          errorRate: 0,
          avgDurationMs: 876,
          cacheReadTokens: 0,
          billableTokens: 358_023,
          cacheSavingsUsd: 0,
        },
        'claude-3-sonnet': {
          requests: 224,
          promptTokens: 567_890,
          completionTokens: 234_567,
          totalTokens: 802_457,
          cost: 7.76,
          costPer1KTokens: 0.0097,
          tokensPerRequest: 3582,
          errorRate: 1.2,
          avgDurationMs: 1567,
          cacheReadTokens: 0,
          billableTokens: 802_457,
          cacheSavingsUsd: 0,
        },
      },
      errors: 12,
      timeRange: {
        from: new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString(),
        to: new Date().toISOString(),
      },
    };
  }

  async getEmbeddingsUsage(_options?: DateRangeOptions): Promise<EmbeddingsUsageStats> {
    return {
      totalRequests: 3456,
      totalTextLength: 1_234_567,
      totalCost: 2.47,
      errors: 8,
      avgDurationMs: 234,
      batchRequests: 456,
      singleRequests: 3000,
      avgBatchSize: 15.3,
    };
  }

  async getLLMDailyStats(_options?: DateRangeOptions): Promise<DailyStats[]> {
    return this.generateMockDailyStats({
      totalTokens: [10000, 60000],
      totalCost: [1, 6],
      avgDurationMs: [500, 2500],
    });
  }

  async getEmbeddingsDailyStats(_options?: DateRangeOptions): Promise<DailyStats[]> {
    return this.generateMockDailyStats({
      totalTokens: [5000, 30000],
      totalCost: [0.1, 0.5],
      avgDurationMs: [100, 500],
    });
  }

  /**
   * Helper to generate mock daily stats
   */
  private generateMockDailyStats(metricsRanges: Record<string, [number, number]>): DailyStats[] {
    const days = 7;
    const stats: DailyStats[] = [];
    const now = new Date();

    for (let i = days - 1; i >= 0; i--) {
      const date = new Date(now);
      date.setDate(date.getDate() - i);
      const dateStr = date.toISOString().split('T')[0]!;

      const metrics: Record<string, number> = {};
      for (const [key, [min, max]] of Object.entries(metricsRanges)) {
        metrics[key] = Math.floor(Math.random() * (max - min)) + min;
      }

      stats.push({
        date: dateStr,
        count: Math.floor(Math.random() * 100) + 50, // 50-150 requests
        metrics,
      });
    }

    return stats;
  }
}
