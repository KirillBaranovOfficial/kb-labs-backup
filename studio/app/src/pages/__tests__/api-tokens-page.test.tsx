/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ApiTokensPage } from '../api-tokens-page.js';
import { _setAuthClientForTesting } from '../../auth/shared-client.js';
import type { AuthHttpClient } from '../../auth/http-client.js';

describe('ApiTokensPage', () => {
  afterEach(() => _setAuthClientForTesting(null));

  it('creates a token, shows the secret once, and lists owner metadata', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce({ tokens: [] })
      .mockResolvedValueOnce({ token: 'kbl_pat_once' })
      .mockResolvedValueOnce({ tokens: [{ tokenId: 'tid-1', name: 'nightly CI', issuedAt: 1000, expiresAt: 2000 }] });
    _setAuthClientForTesting({ fetch } as unknown as AuthHttpClient);
    render(<ApiTokensPage />);
    fireEvent.change(screen.getByLabelText('Token name'), { target: { value: 'nightly CI' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() => expect(screen.getByText('kbl_pat_once')).toBeTruthy());
    expect(screen.getByText(/nightly CI · expires/)).toBeTruthy();
    expect(fetch).toHaveBeenCalledWith('/auth/tokens', expect.objectContaining({ method: 'POST' }));
    expect(JSON.parse(fetch.mock.calls[1]?.[1]?.body as string)).toEqual({ name: 'nightly CI' });
  });

  it('creates a token with the selected optional expiry', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce({ tokens: [] })
      .mockResolvedValueOnce({ token: 'kbl_pat_expiring' })
      .mockResolvedValueOnce({ tokens: [] });
    _setAuthClientForTesting({ fetch } as unknown as AuthHttpClient);
    render(<ApiTokensPage />);
    fireEvent.change(screen.getByLabelText('Token name'), { target: { value: 'deploy bot' } });
    fireEvent.change(screen.getByLabelText('Expiry'), { target: { value: '90' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() => expect(screen.getByText('kbl_pat_expiring')).toBeTruthy());
    expect(JSON.parse(fetch.mock.calls[1]?.[1]?.body as string)).toEqual({ name: 'deploy bot', expiresInDays: 90 });
  });

  it('revokes a token from the owner list', async () => {
    const token = { tokenId: 'tid-2', name: 'old CI', issuedAt: 1000, expiresAt: 2000 };
    const fetch = vi.fn()
      .mockResolvedValueOnce({ tokens: [token] })
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ tokens: [{ ...token, revokedAt: 1500 }] });
    _setAuthClientForTesting({ fetch } as unknown as AuthHttpClient);
    render(<ApiTokensPage />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(screen.getByText(/revoked/i)).toBeTruthy());
    expect(fetch).toHaveBeenCalledWith('/auth/tokens/tid-2/revoke', { method: 'POST' });
  });
});
