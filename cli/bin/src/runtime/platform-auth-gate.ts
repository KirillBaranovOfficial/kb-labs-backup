import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';

export interface PlatformAuthGateInput {
  accessMode: unknown;
  requiresPlatformAuth: boolean;
  hasAuth: boolean;
}

/** Return the catalog auth error when a declared command cannot run in secured mode. */
export function platformAuthFailure(input: PlatformAuthGateInput): ErrorEnvelope | null {
  if (input.accessMode !== 'secured' || !input.requiresPlatformAuth || input.hasAuth) {
    return null;
  }
  return createErrorEnvelope('KB_AUTH_REQUIRED');
}

export async function dispatchWithPlatformAuthGate<T>(
  input: PlatformAuthGateInput,
  dispatch: () => Promise<T>,
): Promise<{ error: ErrorEnvelope } | { value: T }> {
  const error = platformAuthFailure(input);
  if (error) { return { error }; }
  return { value: await dispatch() };
}
