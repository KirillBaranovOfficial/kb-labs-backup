import { isUxResult } from '@kb-labs/core-platform/ux-result';
import { createErrorEnvelope, ErrorEnvelopeError, isErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';

export function requiredAuthEnvelope(gatewayUrl: string): ErrorEnvelope {
  return createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl } });
}

/** Fill catalog template values known to the CLI without changing the error code or copy. */
export function withGatewayUrl(envelope: ErrorEnvelope, gatewayUrl?: string): ErrorEnvelope {
  const resolvedGatewayUrl = gatewayUrl ?? envelope.details?.gatewayUrl;
  if (!resolvedGatewayUrl || resolvedGatewayUrl === '<url>') { return envelope; }
  const fill = (value: string) => value.replaceAll('{gatewayUrl}', resolvedGatewayUrl).replaceAll('<url>', resolvedGatewayUrl);
  return {
    ...envelope,
    message: fill(envelope.message),
    hint: fill(envelope.hint),
    details: { ...envelope.details, gatewayUrl: resolvedGatewayUrl },
  };
}

export class GatewayAuthResponseError extends Error {
  readonly envelope?: ErrorEnvelope;

  constructor(readonly operation: string, readonly status: number, body: string, gatewayUrl?: string) {
    super(`${operation} failed (${status}).`);
    this.name = 'GatewayAuthResponseError';
    try {
      const parsed: unknown = JSON.parse(body);
      if (isUxResult(parsed) && !parsed.ok && isErrorEnvelope(parsed.error)) {
        this.envelope = withGatewayUrl(parsed.error, gatewayUrl);
      }
    } catch (error) {
      if (!(error instanceof SyntaxError)) { throw error; }
      // A non-JSON response retains the existing status-based handling.
    }
  }
}

/** Decode the canonical success envelope returned by Gateway JSON routes. */
export async function readGatewaySuccessData<T>(response: Response): Promise<T> {
  const result: unknown = await response.json();
  if (!isUxResult(result)) {
    throw new Error('Gateway returned an invalid result envelope. Check the Gateway version and retry.');
  }
  if (!result.ok) {
    const responseGatewayUrl = response.url ? new URL(response.url).origin : undefined;
    throw new ErrorEnvelopeError(withGatewayUrl(result.error, responseGatewayUrl));
  }
  return result.data as T;
}
