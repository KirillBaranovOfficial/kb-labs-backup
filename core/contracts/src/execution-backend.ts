/**
 * @module @kb-labs/core-contracts
 *
 * Execution backend interfaces - contract for execution layer.
 */

/**
 * Health status for execution backend.
 */
export const RESOLVED_EXECUTION_MODES = [
  'in-process',
  'subprocess',
  'worker-pool',
  'container',
] as const;

/** Execution modes intentionally unavailable until the roadmap release. */
export const ROADMAP_EXECUTION_MODES = ['remote'] as const;

/** Concrete local execution mode reported by a plane. */
export type ResolvedExecutionMode = (typeof RESOLVED_EXECUTION_MODES)[number];

export interface HealthStatus {
  /** Backend is healthy */
  healthy: boolean;

  /** Backend type */
  backend: ResolvedExecutionMode;

  /** Additional health details */
  details?: Record<string, unknown>;
}

/**
 * Execution statistics.
 */
export interface ExecutionStats {
  /** Total executions */
  totalExecutions: number;

  /** Successful executions */
  successCount: number;

  /** Failed executions */
  errorCount: number;

  /** Average execution time in ms */
  avgExecutionTimeMs: number;

  /** P95 execution time in ms */
  p95ExecutionTimeMs?: number;

  /** P99 execution time in ms */
  p99ExecutionTimeMs?: number;
}
