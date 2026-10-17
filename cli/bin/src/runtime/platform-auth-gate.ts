import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { isPlatformAuthExemptFlag } from './platform-auth-policy.js';

export interface PlatformAuthGateInput {
  accessMode: unknown;
  commandPath: string;
  commandKind: 'system' | 'plugin';
  requiresSession: boolean;
  hasAuth: boolean;
  gatewayUrl?: string;
  authError?: ErrorEnvelope;
  help?: boolean;
}

export function platformAuthFailure(input: PlatformAuthGateInput): ErrorEnvelope | null {
  if (input.accessMode !== 'secured' || (input.help && isPlatformAuthExemptFlag('help')) || !input.requiresSession || input.hasAuth) {
    return null;
  }
  if (input.authError) { return input.authError; }
  return input.gatewayUrl
    ? createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: input.gatewayUrl } })
    : createErrorEnvelope('KB_AUTH_REQUIRED', {
      details: { gatewayUrl: 'unconfigured' },
      hint: 'Run "kb auth login --help" to configure the Gateway URL and credentials.',
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
