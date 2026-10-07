import { CredentialsManager } from './credentials.js';
import { SessionManager } from './session.js';
import { createErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { randomUUID } from 'node:crypto';
import { GatewayAuthResponseError, requiredAuthEnvelope, withGatewayUrl } from './result.js';
import { SavedAuthFileError, savedAuthFileErrorEnvelope } from './saved-auth-file.js';
import { isGatewayConnectionFailure } from './connection-errors.js';
import type { GatewayCredentials, SessionCredentials } from './types.js';
import type { GatewayRequestTarget } from './api-client.js';
import { authRequiredForGateway, credentialMatchesGatewayOrigin } from './auth-origin.js';

/**
 * Return whether a saved human session or machine credential can authenticate
 * platform requests. A saved credential is accepted only by its issuing origin.
 */
let verification: { key: string; result: Promise<PlatformAuthCheck> } | undefined;

export interface PlatformAuthCheck {
  authenticated: boolean;
  error?: ErrorEnvelope;
}

/** Load, refresh once, and verify the process session at most once per CLI invocation. */
export function hasUsablePlatformAuth(target?: GatewayRequestTarget): Promise<PlatformAuthCheck> {
  const key = `${target?.gatewayUrl ?? ''}\n${target?.platformRoot ?? ''}`;
  if (!verification || verification.key !== key) {
    verification = { key, result: verifySavedAuth(target) };
  }
  return verification.result;
}

async function verifySavedAuth(target?: GatewayRequestTarget): Promise<PlatformAuthCheck> {
  const sessions = new SessionManager();
  let session: SessionCredentials | null;
  try {
    session = await sessions.load();
  } catch (error) {
    return classifyFailure(error);
  }
  if (session) {
    const gatewayUrl = target?.gatewayUrl ?? session.gatewayUrl;
    if (!credentialMatchesGatewayOrigin(session.gatewayUrl, gatewayUrl)) {
      return { authenticated: false, error: authRequiredForGateway(gatewayUrl).envelope };
    }
    try {
      const current = sessions.isExpired(session) ? await sessions.refresh(session) : session;
      return await verifies(gatewayUrl, current.accessToken, target?.platformRoot);
    } catch (error) {
      return classifyFailure(error, gatewayUrl, target?.platformRoot);
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
  const gatewayUrl = target?.gatewayUrl ?? machine.gatewayUrl;
  if (!credentialMatchesGatewayOrigin(machine.gatewayUrl, gatewayUrl)) {
    return { authenticated: false, error: authRequiredForGateway(gatewayUrl).envelope };
  }
  try {
    const current = credentials.isExpired(machine) ? await credentials.refresh(machine) : machine;
    return await verifies(gatewayUrl, current.accessToken, target?.platformRoot);
  } catch (error) {
    return classifyFailure(error, gatewayUrl, target?.platformRoot);
  }
}

async function verifies(gatewayUrl: string, accessToken: string, platformRoot?: string): Promise<PlatformAuthCheck> {
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
    return classifyFailure(error, gatewayUrl, platformRoot);
  }
}

function classifyFailure(error: unknown, gatewayUrl?: string, platformRoot?: string): PlatformAuthCheck {
  if (error instanceof SavedAuthFileError) {
    return { authenticated: false, error: savedAuthFileErrorEnvelope(error) };
  }
  if (isGatewayConnectionFailure(error) && gatewayUrl) {
    return {
      authenticated: false,
      error: createErrorEnvelope('KB_HOST_UNREACHABLE', {
        details: { address: new URL(gatewayUrl).origin, ...(platformRoot ? { platformRoot } : {}) },
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
