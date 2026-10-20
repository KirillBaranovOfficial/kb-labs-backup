import type { ExecutionMetadata, ExecutionError, ErrorEnvelope } from '@kb-labs/core-platform';

const NORMALIZED_RESULT: unique symbol = Symbol('normalized-execution-result');

export interface NormalizedExecutionFailure extends ExecutionError {
  envelope: ErrorEnvelope;
}

/** Public result type created only by the factory normalization boundary. */
export type NormalizedExecutionResult = ( {
  ok: true;
  data?: unknown;
  executionTimeMs: number;
  artifactIds?: string[];
  metadata?: ExecutionMetadata;
  [key: string]: unknown;
} | {
  ok: false;
  error: NormalizedExecutionFailure;
  executionTimeMs: number;
  metadata?: ExecutionMetadata;
  [key: string]: unknown;
}) & {
  readonly [NORMALIZED_RESULT]: true;
};

type BeforeNormalization<T> = T extends unknown ? Omit<T, typeof NORMALIZED_RESULT> : never;

/** Internal to this package; the public package entry exports only the type. */
export function brandNormalizedExecutionResult<T extends BeforeNormalization<NormalizedExecutionResult>>(
  result: T,
) {
  return Object.assign(result, { [NORMALIZED_RESULT]: true as const });
}

/** Identify results created at this package's normalization boundary. */
export function isFactoryNormalizedExecutionResult(
  result: unknown,
): result is NormalizedExecutionResult {
  return typeof result === 'object' && result !== null && Reflect.get(result, NORMALIZED_RESULT) === true;
}
