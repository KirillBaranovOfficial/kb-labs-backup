/** @module @kb-labs/studio-app/components/ErrorBoundary */

import * as React from 'react';
import { useRouteError, isRouteErrorResponse, Link } from 'react-router-dom';
import { UIButton, UICard, UITypographyText, UITypographyParagraph, UISpace } from '@kb-labs/studio-ui-kit';
import { normalizeStudioFailure, recordStudioFailure } from '@kb-labs/studio-federation';

export function ErrorBoundary() {
  const routeError = useRouteError();
  const error = isRouteErrorResponse(routeError) ? routeError.data : routeError;
  const failure = React.useMemo(() => normalizeStudioFailure(error), [error]);
  const [copied, setCopied] = React.useState(false);

  React.useEffect(() => {
    recordStudioFailure('Studio route failed', failure);
  }, [failure]);

  const copyDiagnostics = async () => {
    await navigator.clipboard.writeText(`${failure.code}\n${failure.correlationId}`);
    setCopied(true);
  };

  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      minHeight: '100vh',
      padding: '2rem',
      backgroundColor: 'var(--bg-tertiary)',
    }}>
      <UICard style={{ maxWidth: 640, width: '100%', borderRadius: 16, backgroundColor: 'var(--bg-secondary)' }}>
        <UISpace direction="vertical" size="middle" style={{ width: '100%' }}>
          <UITypographyText strong style={{ fontSize: '1.5rem' }}>Studio could not open this page</UITypographyText>
          <UITypographyParagraph style={{ color: 'var(--text-secondary)', margin: 0 }}>
            {failure.message}
          </UITypographyParagraph>
          <UITypographyText type="secondary">{failure.hint}</UITypographyText>
          <UITypographyText type="secondary">Code: {failure.code}</UITypographyText>
          <UITypographyText type="secondary">Correlation ID: {failure.correlationId}</UITypographyText>
          <UISpace>
            <Link to="/"><UIButton variant="primary">Go to dashboard</UIButton></Link>
            <UIButton onClick={() => window.location.reload()}>Retry</UIButton>
            <UIButton onClick={copyDiagnostics}>{copied ? 'Copied' : 'Copy diagnostics'}</UIButton>
          </UISpace>
        </UISpace>
      </UICard>
    </div>
  );
}
