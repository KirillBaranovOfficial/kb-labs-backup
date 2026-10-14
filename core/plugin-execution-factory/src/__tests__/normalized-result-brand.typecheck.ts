import type { ExecutionBackend, NormalizedExecutionResult } from '../types.js';

// This compile-time assertion keeps structural backend values out of consumers.
// @ts-expect-error a structurally matching result has not crossed the factory boundary
const forgedResult: NormalizedExecutionResult = { ok: true, executionTimeMs: 0 };
void forgedResult;

const forgedBackend = {
  execute: async () => ({ ok: true as const, executionTimeMs: 0 }),
  health: async () => ({ healthy: true, backend: 'in-process' as const }),
  stats: async () => ({ totalExecutions: 0, successCount: 0, errorCount: 0, avgExecutionTimeMs: 0 }),
  shutdown: async () => {},
};
// @ts-expect-error a structural backend cannot manufacture the private result brand
const unwrappedBackend: ExecutionBackend = forgedBackend;
void unwrappedBackend;
