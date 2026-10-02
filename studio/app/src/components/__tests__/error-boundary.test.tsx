// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { render, screen } from '@testing-library/react';
import { ErrorBoundary } from '../error-boundary';

const { useRouteError } = vi.hoisted(() => ({ useRouteError: vi.fn() }));

vi.mock('react-router-dom', () => ({
  useRouteError,
  isRouteErrorResponse: () => false,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

vi.mock('@kb-labs/studio-federation', async () => {
  const actual = await vi.importActual<typeof import('@kb-labs/studio-federation')>('@kb-labs/studio-federation');
  return { ...actual, recordStudioFailure: vi.fn() };
});

describe('Studio route error boundary', () => {
  beforeEach(() => {
    useRouteError.mockReset();
  });

  // case: S-203.5
  it('renders a safe unexpected failure and omits its raw message and stack', () => {
    useRouteError.mockReturnValue(new Error('private raw message'));

    render(<ErrorBoundary />);

    expect(document.body.textContent).toContain('Code: KB_RUNTIME_UNEXPECTED');
    expect(screen.getByText('Copy diagnostics')).toBeTruthy();
    expect(document.body.textContent).toMatch(/Correlation ID: studio-/);
    expect(document.body.textContent).not.toContain('private raw message');
    expect(document.body.textContent).not.toContain('Error: private raw message');
  });
});
