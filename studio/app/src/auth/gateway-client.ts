import { decodeUxSuccessResult } from '@kb-labs/studio-data-client';

/** Decode the canonical Gateway success envelope used by Studio auth routes. */
export async function readGatewaySuccess<T>(response: Response): Promise<T> {
  return decodeUxSuccessResult<T>(await response.json());
}
