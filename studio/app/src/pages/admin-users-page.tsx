/**
 * @module @kb-labs/studio-app/pages/admin-users-page
 *
 * Admin users management page (ADR-0020, Phase 2.8).
 * Requires USERS_READ permission to view; USERS_WRITE to disable/enable.
 *
 * All mutations use the shared authClient which injects CSRF headers automatically.
 */

import * as React from 'react';
import { RequirePermission } from '@/auth/guards';
import { authClient } from '../auth/shared-client.js';

interface UserRecord {
  userId: string;
  email: string;
  tenantId: string;
  status: 'active' | 'disabled' | 'pending';
}

interface UsersResponse { users: UserRecord[]; }

function UsersList() {
  const [users, setUsers] = React.useState<UserRecord[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [resetMessage, setResetMessage] = React.useState<string | null>(null);

  const loadUsers = React.useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await authClient.fetch<UsersResponse>('/auth/users');
      setUsers(data.users);
    } catch {
      setError('Failed to load users');
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => { void loadUsers(); }, [loadUsers]);

  const handleToggle = async (user: UserRecord) => {
    const action = user.status === 'active' ? 'disable' : 'enable';
    try {
      await authClient.fetch<unknown>(`/auth/users/${user.userId}/${action}`, { method: 'POST' });
      await loadUsers();
    } catch {
      setError(`Failed to ${action} user`);
    }
  };

  const handleResetLink = async (user: UserRecord) => {
    setError(null);
    setResetMessage(null);
    try {
      const result = await authClient.fetch<{ resetUrl: string }>(`/auth/users/${user.userId}/password-reset-link`, { method: 'POST' });
      await navigator.clipboard.writeText(result.resetUrl);
      setResetMessage(`Password reset link for ${user.email} copied to clipboard.`);
    } catch {
      setError(`Could not create a password reset link for ${user.email}.`);
    }
  };

  return (
    <div style={{ maxWidth: '720px', margin: '2rem auto', padding: '0 1rem' }}>
      <h1 style={{ fontSize: '1.5rem', fontWeight: 600, marginBottom: '1.5rem' }}>Users</h1>

      {loading && <p>Loading users…</p>}
      {error && <p role="alert" style={{ color: 'red' }}>{error}</p>}
      {resetMessage && <p role="status">{resetMessage}</p>}

      {!loading && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr>
              <th style={thStyle}>Email</th>
              <th style={thStyle}>Status</th>
              <th style={thStyle}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <tr key={user.userId}>
                <td style={tdStyle}>{user.email}</td>
                <td style={tdStyle}>{user.status}</td>
                <td style={tdStyle}>
                  {user.status === 'active' ? (
                    <>
                      <button onClick={() => void handleToggle(user)} style={dangerButtonStyle}>Disable</button>
                      <button onClick={() => void handleResetLink(user)} style={successButtonStyle}>Copy reset link</button>
                    </>
                  ) : (
                    <>
                      <button onClick={() => void handleToggle(user)} style={successButtonStyle}>Enable</button>
                      <button onClick={() => void handleResetLink(user)} style={successButtonStyle}>Copy reset link</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

export function AdminUsersPage() {
  return (
    <RequirePermission permission="users:read">
      <UsersList />
    </RequirePermission>
  );
}

const thStyle: React.CSSProperties = {
  textAlign: 'left',
  padding: '0.5rem 0.75rem',
  borderBottom: '2px solid var(--border-primary, #e0e0e0)',
  fontWeight: 600,
};

const tdStyle: React.CSSProperties = {
  padding: '0.5rem 0.75rem',
  borderBottom: '1px solid var(--border-primary, #e0e0e0)',
};

const dangerButtonStyle: React.CSSProperties = {
  padding: '0.25rem 0.6rem',
  borderRadius: '4px',
  border: '1px solid var(--color-error, #ff4d4f)',
  background: 'transparent',
  color: 'var(--color-error, #ff4d4f)',
  cursor: 'pointer',
  fontSize: '0.8rem',
};

const successButtonStyle: React.CSSProperties = {
  padding: '0.25rem 0.6rem',
  borderRadius: '4px',
  border: '1px solid var(--color-success, #52c41a)',
  background: 'transparent',
  color: 'var(--color-success, #52c41a)',
  cursor: 'pointer',
  fontSize: '0.8rem',
};
