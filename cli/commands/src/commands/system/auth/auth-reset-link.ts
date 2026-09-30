import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { SessionManager } from '@kb-labs/cli-runtime/gateway';

type ResetLinkFlags = { 'user-id': { type: 'string'; description: string }; json: { type: 'boolean'; description: string } };

export const authResetLink = defineSystemCommand<ResetLinkFlags, CommandResult & { resetUrl?: string }>({
  name: 'reset-link',
  description: 'Create an admin password reset link for a user',
  longDescription: 'Creates a one-time password reset link that expires after one hour. Share it with the user out of band.',
  category: 'auth',
  examples: ['kb auth reset-link --user-id usr_123', 'kb auth reset-link --user-id usr_123 --json'],
  flags: {
    'user-id': { type: 'string', description: 'User ID from `kb` user administration' },
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  async handler(ctx, _argv, flags) {
    const fail = (error: string) => {
      if (flags.json) ctx.ui?.json({ ok: false, error });
      else ctx.ui?.error?.(error);
      return { ok: false, error };
    };
    if (!flags['user-id']) return fail('Provide --user-id. Run `kb auth reset-link --user-id <id>` after `kb auth login`.');
    const manager = new SessionManager();
    let session = await manager.load();
    if (!session) return fail('An administrator session is required. Run `kb auth login --gateway-url <url> --email <admin> --password <password>`.');
    if (manager.isExpired(session)) {
      try { session = await manager.refresh(session); }
      catch { return fail('The saved session could not be refreshed. Run `kb auth login` again, then retry `kb auth reset-link`.'); }
    }
    let response: Response;
    try {
      response = await fetch(`${session.gatewayUrl}/auth/users/${encodeURIComponent(flags['user-id'])}/password-reset-link`, {
        method: 'POST', headers: { Authorization: `Bearer ${session.accessToken}` },
      });
    } catch {
      return fail('Could not reach the Gateway. Check the Gateway and retry `kb auth reset-link`.');
    }
    if (!response.ok) {
      return fail(response.status === 403
        ? 'This account cannot create reset links. Sign in as an admin and retry `kb auth reset-link`.'
        : `Gateway could not create the reset link (HTTP ${response.status}). Check the user ID and retry kb auth reset-link.`);
    }
    const result = await response.json() as { resetUrl: string; expiresAt: number };
    if (flags.json) ctx.ui?.json({ ok: true, ...result });
    else ctx.ui?.write?.(`Password reset link (expires ${new Date(result.expiresAt).toISOString()}):\n${result.resetUrl}\n`);
    return { ok: true, resetUrl: result.resetUrl };
  },
});
