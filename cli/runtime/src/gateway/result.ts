import { isUxResult } from '@kb-labs/core-platform/ux-result';

export class GatewayAuthResponseError extends Error {
  constructor(readonly operation: string, readonly status: number, body: string) {
    super(`${operation} failed (${status}): ${body}`);
    this.name = 'GatewayAuthResponseError';
  }
}

/** Decode the canonical success envelope returned by Gateway JSON routes. */
export async function readGatewaySuccessData<T>(response: Response): Promise<T> {
  const result: unknown = await response.json();
  if (!isUxResult(result)) {
    throw new Error('Gateway returned an invalid result envelope. Check the Gateway version and retry.');
  }
  if (!result.ok) {
    const correlation = result.error.correlationId ? ` (correlationId: ${result.error.correlationId})` : '';
    throw new Error(`${result.error.code}: ${result.error.message} — ${result.error.hint}${correlation}`);
  }
  return result.data as T;
}
