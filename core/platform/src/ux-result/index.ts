/** Browser-safe validator for the shared machine result envelope. */

import { validateErrorEnvelope } from '../error-envelope/validate.js';
import type { ErrorEnvelope } from '../error-envelope/types.js';

export interface UxSuccessResult<T = unknown> {
  ok: true;
  data: T;
  meta?: Record<string, unknown>;
}

export interface UxFailureResult {
  ok: false;
  error: ErrorEnvelope;
}

export type UxResult<T = unknown> = UxSuccessResult<T> | UxFailureResult;

type UxResultCandidate = Record<string, unknown> & { ok: unknown };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Detects values that claim to use the result-envelope discriminator. */
export function isUxResultCandidate(value: unknown): value is UxResultCandidate {
  return isRecord(value) && 'ok' in value;
}

/** Returns a list of problems; empty means the value is a valid result envelope. */
export function validateUxResult(value: unknown): string[] {
  if (!isUxResultCandidate(value)) return ['result must be an object with a boolean "ok" field'];
  if (typeof value.ok !== 'boolean') return ['result "ok" field must be a boolean'];

  const problems: string[] = [];
  if (value.ok) {
    for (const key of Object.keys(value)) {
      if (key !== 'ok' && key !== 'data' && key !== 'meta') problems.push(`unknown field "${key}"`);
    }
    if (!('data' in value)) problems.push('success result must include "data"');
    if (value.meta !== undefined && !isRecord(value.meta)) problems.push('meta must be an object');
    return problems;
  }

  for (const key of Object.keys(value)) {
    if (key !== 'ok' && key !== 'error') problems.push(`unknown field "${key}"`);
  }
  problems.push(...validateErrorEnvelope(value.error).map((problem) => `error.${problem}`));
  return problems;
}

export function isUxResult<T = unknown>(value: unknown): value is UxResult<T> {
  return validateUxResult(value).length === 0;
}
