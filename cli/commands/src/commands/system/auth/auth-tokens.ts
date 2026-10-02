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

type TokenFlags = {
  name: { type: 'string'; description: string };
  revoke: { type: 'string'; description: string };
  'expires-in-days': { type: 'string'; description: string };
  json: { type: 'boolean'; description: string };
};

interface TokenRecord {
  tokenId: string;
  name: string;
  issuedAt: number;
  expiresAt?: number;
  revokedAt?: number;
}

export const authTokens = defineSystemCommand<TokenFlags, CommandResult & { tokens?: TokenRecord[]; token?: string }>({
  name: 'tokens',
  scope: 'platform',
  description: 'List, create, or revoke your API tokens',
  longDescription: 'API tokens are personal credentials for agents and CI. Each token inherits your current account permissions. Set an optional expiry from 1 to 365 days.',
  category: 'auth',
  examples: ['kb auth tokens', 'kb auth tokens --name "nightly CI"', 'kb auth tokens --name "nightly CI" --expires-in-days 90', 'kb auth tokens --revoke <token-id>'],
  flags: {
    name: { type: 'string', description: 'Create a token with this name; its secret is shown once' },
    revoke: { type: 'string', description: 'Revoke one of your tokens by token ID' },
    'expires-in-days': { type: 'string', description: 'Expire this token after 1 to 365 days (default: never)' },
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  async handler(ctx, _argv, flags) {
    const fail = (error: string) => {
      if (flags.json) {ctx.ui?.json({ ok: false, error });}
      else {ctx.ui?.error?.(error);}
      return { ok: false, error };
    };
    const request = async (path: string, method = 'GET', body?: Record<string, string | number>): Promise<Response> => gatewayApiFetch(`http://127.0.0.1:4000${path}`, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    const showGatewayError = (error: unknown) => {
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
    };
    try {
      if (flags.revoke) {
        const response = await request(`/auth/tokens/${encodeURIComponent(flags.revoke)}/revoke`, 'POST');
        if (!response.ok) {return fail(`Gateway could not revoke token (HTTP ${response.status}). Check the token ID and retry kb auth tokens --revoke <token-id>.`);}
        if (flags.json) {ctx.ui?.json({ ok: true, revoked: flags.revoke });}
        else {ctx.ui?.write?.(`Revoked API token ${flags.revoke}.\n`);}
        return { ok: true };
      }
      if (flags.name) {
        const expiresFlag = flags['expires-in-days'];
        const days = expiresFlag === undefined ? undefined : Number(expiresFlag);
        if (days !== undefined && (!Number.isInteger(days) || days < 1 || days > 365)) {
          return fail('Expiry must be a whole number from 1 to 365 days. Retry `kb auth tokens --name <name> --expires-in-days 90`.');
        }
        const response = await request('/auth/tokens', 'POST', {
          name: flags.name,
          ...(days === undefined ? {} : { expiresInDays: days }),
        });
        if (!response.ok) {return fail(`Gateway could not create the API token (HTTP ${response.status}). Retry kb auth tokens --name <name>.`);}
        const result = await response.json() as { token: string; tokenRecord: TokenRecord };
        if (flags.json) {ctx.ui?.json({ ok: true, ...result });}
        else {ctx.ui?.write?.(`Token ${result.tokenRecord.name}${result.tokenRecord.expiresAt ? ` (expires ${new Date(result.tokenRecord.expiresAt).toISOString()})` : ' (no expiry)'}:\n${result.token}\n`);}
        return { ok: true, token: result.token };
      }
      const response = await request('/auth/tokens');
      if (!response.ok) {return fail(`Gateway could not list API tokens (HTTP ${response.status}). Retry kb auth tokens.`);}
      const result = await response.json() as { tokens: TokenRecord[] };
      if (flags.json) {ctx.ui?.json({ ok: true, ...result });}
      else if (result.tokens.length === 0) {ctx.ui?.write?.('No API tokens. Create one with `kb auth tokens --name "nightly CI"`.\n');}
      else {ctx.ui?.write?.(result.tokens.map((token) => `${token.tokenId}  ${token.name}  ${token.expiresAt ? `expires ${new Date(token.expiresAt).toISOString()}` : 'no expiry'}${token.revokedAt ? '  revoked' : ''}`).join('\n') + '\n');}
      return { ok: true, tokens: result.tokens };
    } catch (error) {
      return showGatewayError(error);
    }
  },
});
