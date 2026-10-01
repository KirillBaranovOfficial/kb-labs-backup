/**
 * HTTP client for marketplace service via Gateway.
 */

import { useEnv } from '@kb-labs/sdk';
import { gatewayApiFetch } from '@kb-labs/cli-runtime/gateway';

const DEFAULT_GATEWAY_URL = 'http://127.0.0.1:4000';
const MARKETPLACE_PREFIX = '/api/v1/marketplace';
const FETCH_TIMEOUT_MS = 30_000;

function getBaseUrl(): string {
  const marketplaceUrl = useEnv('KB_MARKETPLACE_URL');
  if (marketplaceUrl) {
    return `${marketplaceUrl}${MARKETPLACE_PREFIX}`;
  }
  const gateway = useEnv('KB_GATEWAY_URL') ?? DEFAULT_GATEWAY_URL;
  return `${gateway}${MARKETPLACE_PREFIX}`;
}

async function request<T>(method: string, path: string, body?: Record<string, unknown>, params?: Record<string, string>): Promise<T> {
  const url = new URL(`${getBaseUrl()}${path}`);
  if (params) {
    for (const [key, value] of Object.entries(params)) { url.searchParams.set(key, value); }
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
      const headers: Record<string, string> = {};
      if (body) { headers['Content-Type'] = 'application/json'; }
      const response = await gatewayApiFetch(url, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });
      if (!response.ok) {
        throw new Error(await formatMarketplaceFailure(response));
      }
      if (response.status === 204) { return undefined as T; }
      return await response.json() as T;
  } catch (err) {
      if ((err as Error).name === 'AbortError') {
        throw new Error(
          `Marketplace ${path} timed out. Check the installed platform with \`kb-create status\`; start it with \`kb-create start\` if it is stopped.`,
        );
      }
      throw err;
  } finally {
    clearTimeout(timer);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

async function formatMarketplaceFailure(response: Response): Promise<string> {
  let body: unknown;
  try {
    body = JSON.parse(await response.text()) as unknown;
  } catch {
    body = undefined;
  }
  const error = isRecord(body) && isRecord(body.error) ? body.error : body;
  if (isRecord(error) && typeof error.message === 'string') {
    const hint = typeof error.hint === 'string' ? ` Next: ${error.hint}` : '';
    return `Marketplace request failed (${response.status}): ${error.message}.${hint}`;
  }
  return `Marketplace request failed (${response.status}). Check the installed platform with \`kb-create status\`; start it with \`kb-create start\` if it is stopped.`;
}

export async function post<T = unknown>(path: string, body: Record<string, unknown>): Promise<T> {
  return request<T>('POST', path, body);
}

export async function patch<T = unknown>(path: string, body: Record<string, unknown>): Promise<T> {
  return request<T>('PATCH', path, body);
}

export async function del<T = unknown>(path: string, body?: Record<string, unknown>): Promise<T> {
  return request<T>('DELETE', path, body);
}

export async function get<T = unknown>(path: string, params?: Record<string, string>): Promise<T> {
  return request<T>('GET', path, undefined, params);
}
