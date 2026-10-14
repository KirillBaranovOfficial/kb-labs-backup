/**
 * @module @kb-labs/core-contracts
 *
 * Execution backend interfaces - contract for execution layer.
 */

/**
 * Health status for execution backend.
 */
export interface HealthStatus {
  /** Backend is healthy */
  healthy: boolean;

  /** Backend type */
  backend: "in-process" | "subprocess" | "worker-pool" | "remote";

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
