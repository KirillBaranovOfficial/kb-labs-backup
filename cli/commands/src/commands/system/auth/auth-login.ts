/**
 * kb auth login — Authenticate CLI with Gateway.
 *
 * Two identities, one command:
 * - `--client-id`/`--client-secret` (machine): exchanges credentials for a
 *   JWT pair via POST /auth/token, saves to ~/.kb/credentials.json.
 * - `--email`/`--password` (human): exchanges credentials for a JWT pair via
 *   POST /auth/login/cli (the CLI-only, no-cookies analogue of the browser
 *   /auth/login — see services/gateway/app/src/auth/user-routes.ts), saves
 *   to ~/.kb/session.json via SessionManager. This is what lets admin-gated
 *   actions like "kb auth register" work from the CLI at all — a machine
 *   token alone can never carry MACHINE_REGISTER (see auth-register.ts).
 *
 * Both branches auto-refresh on expiry and chmod their store 0o600.
 */

import { defineSystemCommand, throwSystemCommandError, type CommandResult } from '@kb-labs/shared-command-kit';
import { CredentialsManager, readGatewaySuccessData, SessionManager } from '@kb-labs/cli-runtime/gateway';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import { promptForPassword, readPasswordFromStdin } from './password-input.js';

type LoginFlags = {
  'gateway-url': { type: 'string'; description: string };
  'client-id': { type: 'string'; description: string };
  'client-secret': { type: 'string'; description: string };
  email: { type: 'string'; description: string };
  password: { type: 'string'; description: string };
  'password-stdin': { type: 'boolean'; description: string };
  json: { type: 'boolean'; description: string };
};

type LoginResult = CommandResult & {
  gatewayUrl: string;
  expiresIn: number;
  handle?: string;
  email?: string;
  tenantId?: string;
};

export const authLogin = defineSystemCommand<LoginFlags, LoginResult>({
  name: 'login',
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command creates an authentication session." },
  description: 'Authenticate CLI with Gateway',
  longDescription:
    'Machine identity: exchange --client-id/--client-secret for JWT tokens, saved to ' +
    '~/.kb/credentials.json. Use "kb auth register" to create credentials first. ' +
    'Human identity: exchange --email and a prompted or stdin password for JWT tokens, saved to ' +
    '~/.kb/session.json. Both auto-refresh on expiry.',
  category: 'auth',
  examples: [
    'kb auth login --gateway-url <gateway-url> --client-id clt_xxx --client-secret cs_xxx',
    'kb auth login --gateway-url <gateway-url> --email admin@example.com',
    'kb auth login --gateway-url <gateway-url> --email admin@example.com --password-stdin < password.txt',
    'kb auth login --gateway-url https://gateway.example.com --client-id clt_xxx --client-secret cs_xxx --json',
  ],
  flags: {
    'gateway-url': { type: 'string', description: 'Gateway server URL' },
    'client-id': { type: 'string', description: 'Client ID from service account registration' },
    'client-secret': { type: 'string', description: 'Client Secret from service account registration' },
    email: { type: 'string', description: 'Email for human (admin) login' },
    password: { type: 'string', description: 'Password for human (admin) login' },
    'password-stdin': { type: 'boolean', description: 'Read the human password from stdin' },
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  // eslint-disable-next-line sonarjs/cognitive-complexity
  async handler(ctx, _argv, flags) {
    const gatewayUrl = flags['gateway-url'];
    const clientId = flags['client-id'];
    const clientSecret = flags['client-secret'];
    const email = flags.email;

    if (!gatewayUrl) {
      const msg = '--gateway-url is required, plus either --client-id/--client-secret or --email';
      throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
    }

    const passwordStdinCommand = `cat password.txt | kb auth login --gateway-url ${shellQuote(gatewayUrl)} --email ${shellQuote(email ?? '')} --password-stdin`;

    if (flags['password-stdin'] && flags.password) {
      throwSystemCommandError('Choose either --password or --password-stdin.', 'Run `kb auth login --help` and retry with one password source.');
    }

    let password = flags.password;
    if (!password && flags['password-stdin']) {
      password = await readPasswordFromStdin();
    } else if (!password && email) {
      password = await promptForPassword(passwordStdinCommand);
    }

    if (email && !password) {
      throwSystemCommandError(
        'No password was provided for the human login.',
        passwordStdinCommand,
      );
    }

    if (email && password) {
      return humanLogin(ctx, flags, gatewayUrl, email, password);
    }

    if (!clientId || !clientSecret) {
      const msg = 'Provide either --client-id/--client-secret (machine) or --email with a password (human)';
      throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
    }

    // Exchange credentials for tokens
    const tokenUrl = `${gatewayUrl}/auth/token`;
    let tokenResponse: Response;
    try {
      tokenResponse = await fetch(tokenUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ clientId, clientSecret }),
      });
    } catch (err) {
      const msg = `Cannot reach Gateway at ${gatewayUrl}: ${err instanceof Error ? err.message : String(err)}`;
      throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
    }

    if (!tokenResponse.ok) {
      const body = await tokenResponse.text().catch(() => '');
      const msg = `Authentication failed (HTTP ${tokenResponse.status}): ${body}`;
      throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
    }

    const tokenData = await readGatewaySuccessData<{
      accessToken: string;
      refreshToken: string;
      expiresIn: number;
    }>(tokenResponse);

    // Fetch profile (handle, namespaceId) via /auth/me
    let handle: string | undefined;
    let namespaceId: string | undefined;
    try {
      const meRes = await fetch(`${gatewayUrl}/auth/me`, {
        headers: { Authorization: `Bearer ${tokenData.accessToken}` },
      });
      if (meRes.ok) {
        const profile = await readGatewaySuccessData<{ handle?: string; namespaceId?: string }>(meRes);
        handle = profile.handle;
        namespaceId = profile.namespaceId;
      }
    } catch {
      // /auth/me failure is non-fatal — credentials still saved without profile
    }

    // Save credentials
    const credentialsManager = new CredentialsManager();
    const expiresAt = Date.now() + tokenData.expiresIn * 1000;

    await credentialsManager.save({
      gatewayUrl,
      accessToken: tokenData.accessToken,
      refreshToken: tokenData.refreshToken,
      expiresAt,
      handle,
      namespaceId,
    });

    if (!flags.json) {
      ctx.ui?.write?.(`Authenticated with Gateway at ${gatewayUrl}\n`);
      if (handle) { ctx.ui?.write?.(`Handle: ${handle}\n`); }
      ctx.ui?.write?.(`Token expires in ${Math.floor(tokenData.expiresIn / 60)} minutes (auto-refresh enabled).\n`);
    }

    return { ok: true, gatewayUrl, expiresIn: tokenData.expiresIn, ...(handle ? { handle } : {}) };
  },
});

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

/** Human (email/password) login branch — see the module docblock. */
async function humanLogin(
  ctx: PluginContextV3,
  flags: { json?: boolean },
  gatewayUrl: string,
  email: string,
  password: string,
): Promise<LoginResult> {
  let loginResponse: Response;
  try {
    loginResponse = await fetch(`${gatewayUrl}/auth/login/cli`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
  } catch (err) {
    const msg = `Cannot reach Gateway at ${gatewayUrl}: ${err instanceof Error ? err.message : String(err)}`;
    throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
  }

  if (!loginResponse.ok) {
    const body = await loginResponse.text().catch(() => '');
    const msg = `Authentication failed (HTTP ${loginResponse.status}): ${body}`;
    throwSystemCommandError(msg, "Run `kb auth login --help` to review valid inputs and recovery steps, then retry.");
  }

  const loginData = await readGatewaySuccessData<{
    accessToken: string;
    refreshToken: string;
    expiresIn: number;
    tenantId?: string;
  }>(loginResponse);

  // Fetch profile via /auth/me for parity with the machine branch — non-fatal
  // on failure, the session is still saved without a resolved email/tenantId.
  let profileEmail: string | undefined = email;
  let tenantId: string | undefined = loginData.tenantId;
  try {
    const meRes = await fetch(`${gatewayUrl}/auth/me`, {
      headers: { Authorization: `Bearer ${loginData.accessToken}` },
    });
    if (meRes.ok) {
      const profile = await readGatewaySuccessData<{ email?: string; tenantId?: string }>(meRes);
      profileEmail = profile.email ?? profileEmail;
      tenantId = profile.tenantId ?? tenantId;
    }
  } catch {
    // /auth/me failure is non-fatal — session still saved without profile
  }

  const sessionManager = new SessionManager();
  const expiresAt = Date.now() + loginData.expiresIn * 1000;

  await sessionManager.save({
    gatewayUrl,
    accessToken: loginData.accessToken,
    refreshToken: loginData.refreshToken,
    expiresAt,
    email: profileEmail,
    tenantId,
  });

  if (!flags.json) {
    ctx.ui?.write?.(`Authenticated with Gateway at ${gatewayUrl}\n`);
    if (profileEmail) { ctx.ui?.write?.(`Email: ${profileEmail}\n`); }
    ctx.ui?.write?.(`Token expires in ${Math.floor(loginData.expiresIn / 60)} minutes (auto-refresh enabled).\n`);
  }

  return {
    ok: true,
    gatewayUrl,
    expiresIn: loginData.expiresIn,
    ...(profileEmail ? { email: profileEmail } : {}),
    ...(tenantId ? { tenantId } : {}),
  };
}
