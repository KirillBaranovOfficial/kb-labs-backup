import type { ExecutionBackend, NormalizedExecutionResult } from '../index.js';
import type { ErrorEnvelope } from '@kb-labs/core-platform';

// This compile-time assertion keeps structural backend values out of consumers.
declare const forgedEnvelope: ErrorEnvelope;
const forgedResult: NormalizedExecutionResult = {
  ok: false,
  executionTimeMs: 0,
  // @ts-expect-error a plain normalized marker cannot replace the private factory brand
  error: { normalized: true, envelope: forgedEnvelope },
};
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
