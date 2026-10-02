import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { gatewayApiFetch } from '@kb-labs/cli-runtime/gateway';
import type { ErrorEnvelope } from '@kb-labs/core-platform';

function errorEnvelope(error: unknown): ErrorEnvelope | null {
  if (typeof error !== 'object' || error === null || !('envelope' in error)) { return null; }
  const envelope = error.envelope;
  return typeof envelope === 'object' && envelope !== null && 'code' in envelope && 'message' in envelope
    ? envelope as ErrorEnvelope
    : null;
}

type ResetLinkFlags = { 'user-id': { type: 'string'; description: string }; json: { type: 'boolean'; description: string } };

export const authResetLink = defineSystemCommand<ResetLinkFlags, CommandResult & { resetUrl?: string }>({
  name: 'reset-link',
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command requests an account reset link." },
  description: 'Create an admin password reset link for a user',
  longDescription: 'Creates a one-time password reset link that expires after 24 hours. Share it with the user out of band.',
  category: 'auth',
  examples: ['kb auth reset-link --user-id usr_123', 'kb auth reset-link --user-id usr_123 --json'],
  flags: {
    'user-id': { type: 'string', description: 'User ID from `kb` user administration' },
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  async handler(ctx, _argv, flags) {
    const fail = (error: string) => {
      if (flags.json) { ctx.ui?.json({ ok: false, error }); }
      else { ctx.ui?.error?.(error); }
      return { ok: false, error };
    };
    if (!flags['user-id']) { return fail('Provide --user-id. Run `kb auth reset-link --user-id <id>` after `kb auth login`.'); }
    try {
      const response = await gatewayApiFetch(`http://127.0.0.1:4000/auth/users/${encodeURIComponent(flags['user-id'])}/password-reset-link`, { method: 'POST' });
      if (!response.ok) { return fail(`Gateway could not create the reset link (HTTP ${response.status}). Check the user ID and retry kb auth reset-link.`); }
      const result = await response.json() as { resetUrl: string; expiresAt: number };
      if (flags.json) { ctx.ui?.json({ ok: true, ...result }); }
      else { ctx.ui?.write?.(`Password reset link (expires ${new Date(result.expiresAt).toISOString()}):\n${result.resetUrl}\n`); }
      return { ok: true, resetUrl: result.resetUrl };
    } catch (error) {
      const envelope = errorEnvelope(error);
      if (envelope) {
        if (flags.json) { ctx.ui?.json({ ok: false, error: envelope }); }
        else {
          const permission = envelope.details?.permission;
          ctx.ui?.error?.(`${envelope.code}: ${envelope.message}${permission ? ` (permission: ${permission})` : ''} ${envelope.hint}`);
        }
        return { ok: false, error: envelope.message };
      }
      return fail(error instanceof Error ? error.message : String(error));
    }
  },
});
