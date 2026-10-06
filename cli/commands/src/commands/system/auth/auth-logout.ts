/**
 * kb auth logout — Remove stored Gateway credentials.
 *
 * Revokes Gateway refresh tokens, then clears both local identity stores.
 */

import { defineSystemCommand, throwSystemCommandError, type CommandResult } from '@kb-labs/shared-command-kit';
import { CredentialsManager, SessionManager } from '@kb-labs/cli-runtime/gateway';

type LogoutFlags = {
  json: { type: 'boolean'; description: string };
};

export const authLogout = defineSystemCommand<LogoutFlags, CommandResult>({
  name: 'logout',
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command revokes an authentication session." },
  description: 'Remove stored Gateway credentials',
  longDescription: 'Revokes saved Gateway refresh tokens, then deletes ~/.kb/credentials.json and ~/.kb/session.json.',
  category: 'auth',
  examples: [
    'kb auth logout',
    'kb auth logout --json',
  ],
  flags: {
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  async handler(ctx, _argv, flags) {
    const credentialsManager = new CredentialsManager();
    const sessionManager = new SessionManager();
    const [existingCredentials, existingSession] = await Promise.all([
      credentialsManager.load(),
      sessionManager.load(),
    ]);

    const savedTokens: Array<{ gatewayUrl: string; refreshToken: string }> = [];
    if (existingSession) {savedTokens.push(existingSession);}
    if (existingCredentials) {savedTokens.push(existingCredentials);}
    for (const identity of savedTokens) {
      let response: Response;
      try {
        response = await fetch(`${identity.gatewayUrl}/auth/logout/cli`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refreshToken: identity.refreshToken }),
        });
      } catch {
        const error = 'Could not reach a Gateway to revoke saved credentials. Check the Gateway and run `kb auth logout` again.';
        throwSystemCommandError(error, 'Check the Gateway connection and retry `kb auth logout`.');
      }
      if (!response.ok) {
        const error = `Gateway rejected token revocation (HTTP ${response.status}). Run 'kb auth status', then 'kb auth logout' again.`;
        throwSystemCommandError(error, 'Run `kb auth status` to inspect the session, then retry `kb auth logout`.');
      }
    }

    if (!existingCredentials && !existingSession) {
      const message = 'No credentials found — already logged out.';
      if (!flags.json) {
        ctx.ui?.write?.(`${message}\n`);
      }
      return { ok: true, message };
    }

    await Promise.all([credentialsManager.clear(), sessionManager.clear()]);

    const message = 'Logged out. Credentials removed.';
    if (!flags.json) {
      ctx.ui?.write?.(`${message}\n`);
    }

    return { ok: true, message };
  },
});
