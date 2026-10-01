import { CredentialsManager } from './credentials.js';
import { SessionManager } from './session.js';

/**
 * Return whether a saved human session or machine credential can authenticate
 * platform requests. Expired credentials are refreshed before they are used.
 */
export async function hasUsablePlatformAuth(): Promise<boolean> {
  const sessions = new SessionManager();
  const session = await sessions.load();
  if (session) {
    try {
      const current = sessions.isExpired(session) ? await sessions.refresh(session) : session;
      return await verifies(current.gatewayUrl, current.accessToken);
    } catch {
      return false;
    }
  }

  const credentials = new CredentialsManager();
  const machine = await credentials.load();
  if (!machine) { return false; }
  try {
    const current = credentials.isExpired(machine) ? await credentials.refresh(machine) : machine;
    return await verifies(current.gatewayUrl, current.accessToken);
  } catch {
    return false;
  }
}

async function verifies(gatewayUrl: string, accessToken: string): Promise<boolean> {
  const response = await fetch(`${gatewayUrl.replace(/\/$/, '')}/auth/me`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  return response.ok;
}
