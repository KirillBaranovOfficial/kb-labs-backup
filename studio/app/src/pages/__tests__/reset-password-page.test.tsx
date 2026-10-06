/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ResetPasswordPage } from '../reset-password-page.js';

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/reset-password/reset-token']}>
      <Routes><Route path="/reset-password/:token" element={<ResetPasswordPage />} /></Routes>
    </MemoryRouter>,
  );
}

async function submitFailure(body: unknown) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: false,
    json: () => Promise.resolve(body),
  }));
  renderPage();
  fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'NewPassword123!' } });
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'NewPassword123!' } });
  fireEvent.click(screen.getByRole('button', { name: 'Set new password' }));
  return screen.findByRole('alert');
}

describe('ResetPasswordPage', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('shows the shared ADR-0049 message from the Gateway envelope', async () => {
    const alert = await submitFailure({ ok: false, error: {
      code: 'KB_AUTH_INVALID_PASSWORD_RESET',
      message: 'This reset link is invalid, expired, or already used.',
      hint: 'Ask an administrator for a new reset link.',
      correlationId: 'reset-correlation',
    } });
    await waitFor(() => expect(alert.textContent).toBe('This reset link is invalid, expired, or already used.'));
  });

  it('does not read the legacy top-level message field', async () => {
    const alert = await submitFailure({ error: 'invalid_password_reset', message: 'legacy response' });
    await waitFor(() => expect(alert.textContent).toBe('The operation failed unexpectedly.'));
  });
});
