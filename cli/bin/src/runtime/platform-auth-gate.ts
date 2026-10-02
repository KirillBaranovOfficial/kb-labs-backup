import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { isPlatformAuthExempt, isPlatformAuthExemptFlag } from './platform-auth-policy.js';

export interface PlatformAuthGateInput {
  accessMode: unknown;
  commandPath: string;
  commandKind: 'system' | 'plugin';
  hasAuth: boolean;
  gatewayUrl?: string;
  help?: boolean;
}

export function platformAuthFailure(input: PlatformAuthGateInput): ErrorEnvelope | null {
  if (input.accessMode !== 'secured' || (input.help && isPlatformAuthExemptFlag('help')) || isPlatformAuthExempt(input.commandPath, input.commandKind) || input.hasAuth) {
    return null;
  }
  return createErrorEnvelope('KB_AUTH_REQUIRED', {
    details: { gatewayUrl: input.gatewayUrl || '<url>' },
  });
}

export async function dispatchWithPlatformAuthGate<T>(
  input: PlatformAuthGateInput,
  dispatch: () => Promise<T>,
): Promise<{ error: ErrorEnvelope } | { value: T }> {
  const error = platformAuthFailure(input);
  if (error) { return { error }; }
  return { value: await dispatch() };
}
