import type { NormalizedExecutionResult as CoreNormalizedExecutionResult } from '@kb-labs/core-contracts';
import type { ErrorEnvelope } from '@kb-labs/core-platform';

const NORMALIZED_RESULT: unique symbol = Symbol('normalized-execution-result');

/** Opaque result type created only by the factory boundary. */
export type FactoryNormalizedExecutionResult = CoreNormalizedExecutionResult<ErrorEnvelope> & {
  readonly [NORMALIZED_RESULT]: true;
};

/** Internal to this package; the public package entry exports only the type. */
export function brandNormalizedExecutionResult(
  result: CoreNormalizedExecutionResult<ErrorEnvelope>,
): FactoryNormalizedExecutionResult {
  const branded = { ...result };
  Object.defineProperty(branded, NORMALIZED_RESULT, { value: true });
  return branded as FactoryNormalizedExecutionResult;
}

/** Identify results created at this package's normalization boundary. */
export function isFactoryNormalizedExecutionResult(
  result: unknown,
): result is FactoryNormalizedExecutionResult {
  return typeof result === 'object' && result !== null && NORMALIZED_RESULT in result &&
    (result as FactoryNormalizedExecutionResult)[NORMALIZED_RESULT] === true;
}
