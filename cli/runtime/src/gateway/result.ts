import { randomUUID } from 'node:crypto';
import { isUxResult } from '@kb-labs/core-platform/ux-result';
import { createErrorEnvelope, ErrorEnvelopeError, toErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';

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

/** Convert a parsed failure response into one canonical envelope without trusting unknown body fields. */
function decodeGatewayFailureBody(
  parsed: unknown,
  status: number,
  context: { operation: string; gatewayUrl: string },
): ErrorEnvelope {
  if (isUxResult(parsed) && !parsed.ok) {
    return withGatewayUrl(toErrorEnvelope(parsed.error), context.gatewayUrl);
  }

  const details = {
    status: String(status),
    operation: context.operation,
    gatewayUrl: context.gatewayUrl,
  };
  return createErrorEnvelope('KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE', {
    details,
    correlationId: randomUUID(),
  });
}

/** The sole decoder for failed Gateway responses. It never includes an untrusted body in an error. */
export async function decodeGatewayFailure(
  response: Response,
  context: { operation: string; gatewayUrl: string },
): Promise<ErrorEnvelope> {
  let parsed: unknown;
  try {
    parsed = await response.json();
  } catch (error) {
    if (!(error instanceof SyntaxError)) { throw error; }
  }
  return decodeGatewayFailureBody(parsed, response.status, context);
}

/** Decode the canonical success envelope returned by Gateway JSON routes. */
export async function readGatewaySuccessData<T>(
  response: Response,
  context: { operation: string; gatewayUrl: string },
): Promise<T> {
  if (!response.ok) {
    throw new ErrorEnvelopeError(await decodeGatewayFailure(response, context));
  }
  let result: unknown;
  try {
    result = await response.json();
  } catch (error) {
    if (!(error instanceof SyntaxError)) { throw error; }
    throw new ErrorEnvelopeError(decodeGatewayFailureBody(undefined, response.status, context));
  }
  if (!isUxResult(result)) {
    throw new ErrorEnvelopeError(decodeGatewayFailureBody(result, response.status, context));
  }
  if (!result.ok) {
    throw new ErrorEnvelopeError(decodeGatewayFailureBody(result, response.status, context));
  }
  return result.data as T;
}
