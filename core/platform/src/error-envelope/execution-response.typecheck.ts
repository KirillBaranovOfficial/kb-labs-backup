import type { ErrorEnvelope, ExecutionError, SerializedError } from '../index.js';
import { serializeError } from '../index.js';

declare const rawError: unknown;

// @ts-expect-error A raw thrown value must pass through serializeError before transport.
const invalidExecutionError: ExecutionError['error'] = rawError;
const serialized: SerializedError = serializeError(rawError);
declare const executionError: ExecutionError;
const envelope: ErrorEnvelope = executionError;

void invalidExecutionError;
void serialized;
void envelope;
