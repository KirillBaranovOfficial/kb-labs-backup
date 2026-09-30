import * as React from 'react';
import { useParams } from 'react-router-dom';

export function ResetPasswordPage() {
  const { token } = useParams<{ token: string }>();
  const [password, setPassword] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [error, setError] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(false);

  React.useEffect(() => {
    const meta = document.createElement('meta');
    meta.name = 'referrer';
    meta.content = 'no-referrer';
    document.head.appendChild(meta);
    return () => { document.head.removeChild(meta); };
  }, []);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (password !== confirm) { setError('Passwords do not match'); return; }
    setLoading(true);
    setError(null);
    try {
      const response = await fetch('/api/auth/password/reset', {
        method: 'POST', credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token, newPassword: password }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => ({})) as { message?: string };
        setError(body.message ?? 'This reset link is invalid or expired');
        return;
      }
      window.location.replace('/login?password-reset=success');
    } catch {
      setError('Could not reach the Gateway. Check your connection and try again.');
    } finally {
      setLoading(false);
    }
  };

  return <main style={{ maxWidth: 420, margin: '10vh auto', padding: '2rem', background: 'var(--bg-primary, #fff)', borderRadius: 12 }}>
    <h1>Reset your password</h1>
    <p>Choose a new password for your account.</p>
    <form onSubmit={(event) => { void submit(event); }}>
      <label htmlFor="reset-password">New password</label>
      <input id="reset-password" type="password" autoComplete="new-password" required value={password} onChange={(event) => setPassword(event.target.value)} />
      <label htmlFor="reset-confirm">Confirm password</label>
      <input id="reset-confirm" type="password" autoComplete="new-password" required value={confirm} onChange={(event) => setConfirm(event.target.value)} />
      {error && <p role="alert">{error}</p>}
      <button type="submit" disabled={loading}>{loading ? 'Saving…' : 'Set new password'}</button>
    </form>
  </main>;
}
