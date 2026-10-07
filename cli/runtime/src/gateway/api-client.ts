import { createErrorEnvelope, ErrorEnvelopeError, isErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { randomUUID } from 'node:crypto';
import { CredentialsManager } from './credentials.js';
import { SessionManager } from './session.js';
import type { GatewayCredentials, SessionCredentials } from './types.js';
import { isGatewayConnectionFailure } from './connection-errors.js';
import { SavedAuthFileError, savedAuthFileErrorEnvelope } from './saved-auth-file.js';
import { GatewayAuthResponseError, requiredAuthEnvelope, withGatewayUrl } from './result.js';
import { authRequiredForGateway, credentialMatchesGatewayOrigin } from './auth-origin.js';

export interface GatewayRequestTarget {
  gatewayUrl: string;
  platformRoot?: string;
}

export type GatewayCredentialKind = 'either' | 'session' | 'machine';

type AuthState = {
  gatewayUrl: string;
  accessToken: string;
  refresh: () => Promise<AuthState>;
};

export class GatewayApiError extends ErrorEnvelopeError {
  constructor(envelope: ErrorEnvelope) {
    super(envelope);
    this.name = 'GatewayApiError';
  }
}

async function loadAuth(target?: GatewayRequestTarget, credentialKind: GatewayCredentialKind = 'either'): Promise<AuthState | null> {
  const sessions = new SessionManager();
  if (credentialKind !== 'machine') {
    const session = await sessions.load();
    if (session) {
      const gatewayUrl = target?.gatewayUrl ?? session.gatewayUrl;
      if (!credentialMatchesGatewayOrigin(session.gatewayUrl, gatewayUrl)) { throw authRequiredForGateway(gatewayUrl); }
      const current = sessions.isExpired(session) ? await sessions.refresh(session) : session;
      return state(current, () => sessions.refresh(current), gatewayUrl);
    }
  }
  if (credentialKind === 'session') { return null; }
  const credentials = new CredentialsManager();
  const machine = await credentials.load();
  if (!machine) { return null; }
  const gatewayUrl = target?.gatewayUrl ?? machine.gatewayUrl;
  if (!credentialMatchesGatewayOrigin(machine.gatewayUrl, gatewayUrl)) { throw authRequiredForGateway(gatewayUrl); }
  const current = credentials.isExpired(machine) ? await credentials.refresh(machine) : machine;
  return state(current, () => credentials.refresh(current), gatewayUrl);
}

function state(
  credentials: GatewayCredentials | SessionCredentials,
  refresh: () => Promise<GatewayCredentials | SessionCredentials>,
  gatewayUrl = credentials.gatewayUrl,
): AuthState {
  return {
    gatewayUrl,
    accessToken: credentials.accessToken,
    refresh: async () => state(await refresh(), refresh, gatewayUrl),
  };
}

function authenticatedUrl(input: string, gatewayUrl: string): URL {
  const target = new URL(input);
  const configured = new URL(gatewayUrl);
  target.protocol = configured.protocol;
  target.host = configured.host;
  return target;
}

/** Shared REST path for platform APIs: attach credentials, refresh once, and map auth status codes. */
export async function gatewayApiFetch(
  input: string | URL,
  init: RequestInit = {},
  target?: GatewayRequestTarget,
  credentialKind: GatewayCredentialKind = 'either',
): Promise<Response> {
  const requestGatewayUrl = target?.gatewayUrl ?? new URL(String(input)).origin;
  const requestPlatformRoot = target?.platformRoot;
  let auth: AuthState | null;
  try { auth = await loadAuth(target, credentialKind); }
  catch (error) {
    if (error instanceof ErrorEnvelopeError && error.envelope.code === 'KB_AUTH_REQUIRED') { throw error; }
    throw new GatewayApiError(error instanceof SavedAuthFileError
      ? savedAuthFileErrorEnvelope(error)
      : requiredAuthEnvelope(requestGatewayUrl));
  }
  const send = (current: AuthState | null): Promise<Response> => {
    const destinationGatewayUrl = current?.gatewayUrl ?? target?.gatewayUrl;
    const requestTarget = destinationGatewayUrl
      ? authenticatedUrl(String(input), destinationGatewayUrl)
      : new URL(String(input));
    const headers = new Headers(init.headers);
    headers.delete('Authorization');
    if (current) { headers.set('Authorization', `Bearer ${current.accessToken}`); }
    return fetch(requestTarget, { ...init, headers }).catch((error: unknown) => {
      if (isGatewayConnectionFailure(error)) {
        throw new GatewayApiError(createErrorEnvelope('KB_HOST_UNREACHABLE', {
          details: { address: requestTarget.origin, ...(requestPlatformRoot ? { platformRoot: requestPlatformRoot } : {}) },
          correlationId: randomUUID(),
        }));
      }
      throw error;
    });
  };

  let response = await send(auth);
  if (response.status === 401 && auth) {
    try {
      auth = await auth.refresh();
      response = await send(auth);
    } catch (error) {
      if (error instanceof GatewayAuthResponseError && error.envelope) {
        throw new GatewayApiError(withGatewayUrl(error.envelope, auth.gatewayUrl));
      }
      throw new GatewayApiError(requiredAuthEnvelope(auth.gatewayUrl));
    }
  }
  if (response.status === 401) {
    const gatewayUrl = auth?.gatewayUrl ?? requestGatewayUrl;
    const rejection = new GatewayAuthResponseError('Gateway request', 401, await response.clone().text(), gatewayUrl);
    throw new GatewayApiError(rejection.envelope ?? requiredAuthEnvelope(gatewayUrl));
  }
  if (response.status === 403) {
    const body = await response.clone().json().catch(() => null) as { permission?: unknown; error?: unknown } | null;
    const permission = typeof body?.permission === 'string' ? body.permission : undefined;
    const serverEnvelope = isErrorEnvelope(body?.error) && body.error.code === 'KB_AUTH_FORBIDDEN'
      ? body.error
      : undefined;
    throw new GatewayApiError(serverEnvelope ?? createErrorEnvelope('KB_AUTH_FORBIDDEN', {
      ...(permission ? { details: { permission } } : {}),
    }));
  }
  return response;
}
