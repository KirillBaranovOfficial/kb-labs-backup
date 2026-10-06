/**
 * @module @kb-labs/studio-data-client/hooks/use-adapters
 * React Query hooks for platform adapter analytics
 */

import { useQuery } from '@tanstack/react-query';
import type { AdaptersDataSource, DateRangeOptions } from '../sources/adapters-source';

const DAILY_STATS = 'daily-stats';

/**
 * Hook to fetch LLM usage statistics
 *
 * @param source - Adapters data source
 * @param options - Optional date range filter
 */
export function useAdaptersLLMUsage(source: AdaptersDataSource, options?: DateRangeOptions) {
  return useQuery({
    queryKey: ['adapters', 'llm', 'usage', options?.from, options?.to],
    queryFn: () => source.getLLMUsage(options),
    refetchInterval: 60000, // Auto-refresh every 60s (LLM stats change slower)
    staleTime: 50000,
    retry: 2,
  });
}

/**
 * Hook to fetch Embeddings usage statistics
 *
 * @param source - Adapters data source
 * @param options - Optional date range filter
 */
export function useAdaptersEmbeddingsUsage(source: AdaptersDataSource, options?: DateRangeOptions) {
  return useQuery({
    queryKey: ['adapters', 'embeddings', 'usage', options?.from, options?.to],
    queryFn: () => source.getEmbeddingsUsage(options),
    refetchInterval: 60000,
    staleTime: 50000,
    retry: 2,
  });
}

/**
 * Hook to fetch LLM daily statistics for time-series charts
 *
 * @param source - Adapters data source
 * @param options - Optional date range filter
 */
export function useAdaptersLLMDailyStats(source: AdaptersDataSource, options?: DateRangeOptions) {
  return useQuery({
    queryKey: ['adapters', 'llm', DAILY_STATS, options?.from, options?.to, options?.models],
    queryFn: () => source.getLLMDailyStats(options),
    refetchInterval: 60000, // Daily stats change slower
    staleTime: 50000,
    retry: 2,
  });
}

/**
 * Hook to fetch Embeddings daily statistics for time-series charts
 *
 * @param source - Adapters data source
 * @param options - Optional date range filter
 */
export function useAdaptersEmbeddingsDailyStats(source: AdaptersDataSource, options?: DateRangeOptions) {
  return useQuery({
    queryKey: ['adapters', 'embeddings', DAILY_STATS, options?.from, options?.to],
    queryFn: () => source.getEmbeddingsDailyStats(options),
    refetchInterval: 60000,
    staleTime: 50000,
    retry: 2,
  });
}
