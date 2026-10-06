import { CredentialsManager } from './credentials.js';
import { SessionManager } from './session.js';
import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { randomUUID } from 'node:crypto';
import { GatewayAuthResponseError, requiredAuthEnvelope, withGatewayUrl } from './result.js';
import { SavedAuthFileError, savedAuthFileErrorEnvelope } from './saved-auth-file.js';
import { isGatewayConnectionFailure } from './connection-errors.js';
import type { GatewayCredentials, SessionCredentials } from './types.js';

/**
 * Return whether a saved human session or machine credential can authenticate
 * platform requests. Expired credentials are refreshed before they are used.
 */
let verification: Promise<PlatformAuthCheck> | undefined;

export interface PlatformAuthCheck {
  authenticated: boolean;
  error?: ErrorEnvelope;
}

/** Load, refresh once, and verify the process session at most once per CLI invocation. */
export function hasUsablePlatformAuth(): Promise<PlatformAuthCheck> {
  verification ??= verifySavedAuth();
  return verification;
}

async function verifySavedAuth(): Promise<PlatformAuthCheck> {
  const sessions = new SessionManager();
  let session: SessionCredentials | null;
  try {
    session = await sessions.load();
  } catch (error) {
    return classifyFailure(error);
  }
  if (session) {
    try {
      const current = sessions.isExpired(session) ? await sessions.refresh(session) : session;
      return await verifies(current.gatewayUrl, current.accessToken);
    } catch (error) {
      return classifyFailure(error, session.gatewayUrl);
    }
  }

  const credentials = new CredentialsManager();
  let machine: GatewayCredentials | null;
  try {
    machine = await credentials.load();
  } catch (error) {
    return classifyFailure(error);
  }
  if (!machine) { return { authenticated: false }; }
  try {
    const current = credentials.isExpired(machine) ? await credentials.refresh(machine) : machine;
    return await verifies(current.gatewayUrl, current.accessToken);
  } catch (error) {
    return classifyFailure(error, machine.gatewayUrl);
  }
}

async function verifies(gatewayUrl: string, accessToken: string): Promise<PlatformAuthCheck> {
  try {
    const response = await fetch(`${gatewayUrl.replace(/\/$/, '')}/auth/me`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    if (response.status === 401) {
      return classifyFailure(
        new GatewayAuthResponseError('Gateway verification', 401, await response.text(), gatewayUrl),
        gatewayUrl,
      );
    }
    if (!response.ok) { return { authenticated: false, error: gatewayResponseFailure(response.status) }; }
    return { authenticated: true };
  } catch (error) {
    return classifyFailure(error, gatewayUrl);
  }
}

function classifyFailure(error: unknown, gatewayUrl?: string): PlatformAuthCheck {
  if (error instanceof SavedAuthFileError) {
    return { authenticated: false, error: savedAuthFileErrorEnvelope(error) };
  }
  if (isGatewayConnectionFailure(error) && gatewayUrl) {
    return {
      authenticated: false,
      error: createErrorEnvelope('KB_HOST_UNREACHABLE', {
        details: { address: new URL(gatewayUrl).origin },
        correlationId: randomUUID(),
      }),
    };
  }
  if (error instanceof GatewayAuthResponseError && error.status === 401) {
    return error.envelope
      ? { authenticated: false, error: withGatewayUrl(error.envelope, gatewayUrl) }
      : gatewayUrl
        ? { authenticated: false, error: requiredAuthEnvelope(gatewayUrl) }
        : { authenticated: false };
  }
  return {
    authenticated: false,
    error: error instanceof GatewayAuthResponseError
      ? gatewayResponseFailure(error.status)
      : commandFailed(),
  };
}

function commandFailed(): ErrorEnvelope {
  return createErrorEnvelope('KB_RUNTIME_COMMAND_FAILED', {
    message: 'The Gateway could not verify the saved session.',
    hint: 'Run "kb-create doctor" to check the host, then retry the command.',
  });
}

function gatewayResponseFailure(status: number): ErrorEnvelope {
  return createErrorEnvelope('KB_RUNTIME_COMMAND_FAILED', {
    message: `The Gateway could not verify the saved session (HTTP ${status}).`,
    hint: 'Run "kb-create doctor" to check the host, then retry the command.',
  });
}
