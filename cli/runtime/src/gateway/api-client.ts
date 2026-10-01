import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { CredentialsManager } from './credentials.js';
import { SessionManager } from './session.js';
import type { GatewayCredentials, SessionCredentials } from './types.js';

type AuthState = {
  gatewayUrl: string;
  accessToken: string;
  refresh: () => Promise<AuthState>;
};

export class GatewayApiError extends Error {
  constructor(readonly envelope: ErrorEnvelope) {
    super(envelope.message);
    this.name = 'GatewayApiError';
  }
}

async function loadAuth(): Promise<AuthState | null> {
  const sessions = new SessionManager();
  const session = await sessions.load();
  if (session) {
    const current = sessions.isExpired(session) ? await sessions.refresh(session) : session;
    return state(current, () => sessions.refresh(current));
  }
  const credentials = new CredentialsManager();
  const machine = await credentials.load();
  if (!machine) { return null; }
  const current = credentials.isExpired(machine) ? await credentials.refresh(machine) : machine;
  return state(current, () => credentials.refresh(current));
}

function state(
  credentials: GatewayCredentials | SessionCredentials,
  refresh: () => Promise<GatewayCredentials | SessionCredentials>,
): AuthState {
  return {
    gatewayUrl: credentials.gatewayUrl,
    accessToken: credentials.accessToken,
    refresh: async () => state(await refresh(), refresh),
  };
}

function authenticatedUrl(input: string, gatewayUrl: string): URL {
  const target = new URL(input);
  const configured = new URL(gatewayUrl);
  if (target.hostname === 'localhost' || target.hostname === '127.0.0.1') {
    target.protocol = configured.protocol;
    target.host = configured.host;
  }
  return target;
}

/** Shared REST path for platform APIs: attach credentials, refresh once, and map auth status codes. */
export async function gatewayApiFetch(input: string | URL, init: RequestInit = {}): Promise<Response> {
  let auth: AuthState | null;
  try { auth = await loadAuth(); }
  catch { throw new GatewayApiError(createErrorEnvelope('KB_AUTH_REQUIRED')); }
  const send = (current: AuthState | null): Promise<Response> => {
    const target = current ? authenticatedUrl(String(input), current.gatewayUrl) : new URL(String(input));
    const headers = new Headers(init.headers);
    if (current) { headers.set('Authorization', `Bearer ${current.accessToken}`); }
    return fetch(target, { ...init, headers });
  };

  let response = await send(auth);
  if (response.status === 401 && auth) {
    try {
      auth = await auth.refresh();
      response = await send(auth);
    } catch {
      throw new GatewayApiError(createErrorEnvelope('KB_AUTH_REQUIRED'));
    }
  }
  if (response.status === 401) {
    throw new GatewayApiError(createErrorEnvelope('KB_AUTH_REQUIRED'));
  }
  if (response.status === 403) {
    const body = await response.clone().json().catch(() => null) as { permission?: unknown; error?: { code?: unknown } } | null;
    const permission = typeof body?.permission === 'string' ? body.permission : undefined;
    const serverEnvelope = typeof body?.error === 'object' && body.error !== null && body.error.code === 'KB_AUTH_FORBIDDEN'
      ? body.error as unknown as ErrorEnvelope
      : undefined;
    throw new GatewayApiError(serverEnvelope ?? createErrorEnvelope('KB_AUTH_FORBIDDEN', {
      ...(permission ? { details: { permission } } : {}),
    }));
  }
  return response;
}
