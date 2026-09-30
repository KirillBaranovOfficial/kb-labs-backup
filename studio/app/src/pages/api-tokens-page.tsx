import * as React from 'react';
import { authClient } from '../auth/shared-client.js';

interface ApiToken { tokenId: string; name: string; issuedAt: number; expiresAt: number; revokedAt?: number; }
interface TokenList { tokens: ApiToken[]; }

export function ApiTokensPage() {
  const [tokens, setTokens] = React.useState<ApiToken[]>([]);
  const [name, setName] = React.useState('');
  const [newToken, setNewToken] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const load = React.useCallback(async () => {
    try { setTokens((await authClient.fetch<TokenList>('/auth/tokens')).tokens); }
    catch { setError('Could not load API tokens. Refresh the page and try again.'); }
  }, []);
  React.useEffect(() => { void load(); }, [load]);

  const create = async () => {
    if (!name.trim()) { setError('Enter a name for the API token.'); return; }
    try {
      const result = await authClient.fetch<{ token: string }>('/auth/tokens', { method: 'POST', body: JSON.stringify({ name: name.trim() }) });
      setNewToken(result.token);
      setName('');
      setError(null);
      await load();
    } catch { setError('Could not create the API token. Check your connection and try again.'); }
  };
  const revoke = async (token: ApiToken) => {
    try {
      await authClient.fetch(`/auth/tokens/${token.tokenId}/revoke`, { method: 'POST' });
      await load();
    } catch { setError(`Could not revoke ${token.name}. Refresh the list and try again.`); }
  };

  return <main style={{ maxWidth: 760, margin: '2rem auto', padding: '0 1rem' }}>
    <h1>API tokens</h1>
    <p>Create personal tokens for agents and CI. They use your current account permissions and expire after 90 days.</p>
    {error && <p role="alert">{error}</p>}
    {newToken && <section aria-label="New API token">
      <p>Copy this token now. It will not be shown again.</p>
      <code>{newToken}</code>
      <button onClick={() => void navigator.clipboard.writeText(newToken)}>Copy</button>
    </section>}
    <label htmlFor="api-token-name">Token name</label>
    <input id="api-token-name" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} />
    <button onClick={() => void create()}>Create token</button>
    <h2>Your tokens</h2>
    {tokens.length === 0 ? <p>No API tokens.</p> : <ul>{tokens.map((token) => <li key={token.tokenId}>
      <span>{token.name} · expires {new Date(token.expiresAt).toLocaleDateString()}</span>
      {token.revokedAt ? <span> · revoked</span> : <button onClick={() => void revoke(token)}>Revoke</button>}
    </li>)}</ul>}
  </main>;
}
