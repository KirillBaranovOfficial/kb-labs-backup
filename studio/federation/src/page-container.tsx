import { useState, useEffect, useCallback, useMemo, type ComponentType } from 'react';
import { ConfigProvider } from 'antd';
import { UICard, UISkeleton } from '@kb-labs/studio-ui-kit';
import { loadPageComponent, type PageLoadError } from './widget-loader.js';
import { PageErrorBoundary } from './page-error-boundary.js';
import { PluginErrorUI } from './plugin-error-ui.js';
import { PageContextProvider, type PageContext, getAntDesignTokens, getAntDesignComponents } from '@kb-labs/studio-hooks';
import { EventBusProvider } from '@kb-labs/studio-event-bus';

export interface PageContainerProps {
  remoteName: string;
  entry: string;
  /** Current remoteEntryUrl from registry. Used to detect plugin rebuilds at navigation time. */
  remoteEntryUrl?: string;
  pageId: string;
  pluginId: string;
  config?: Record<string, unknown>;
  permissions?: string[];
}

/**
 * Loads an MF remote page component and wraps it with providers.
 *
 * Lifecycle:
 * 1. Shows the shared Studio page skeleton
 * 2. Loads remote via Module Federation (retries built-in)
 * 3. On load failure → PluginErrorUI (load variant) with retry
 * 4. On runtime crash → PageErrorBoundary catches → PluginErrorUI (crash variant)
 *
 * ConfigProvider is injected here so that plugin pages receive the same
 * Ant Design theme tokens as the studio host (CSS-variable-based theming).
 */
export function PageContainer({
  remoteName,
  entry,
  remoteEntryUrl,
  pageId,
  pluginId,
  config = {},
  permissions = [],
}: PageContainerProps) {
  const [PageComponent, setPageComponent] = useState<ComponentType<unknown> | null>(null);
  const [loadError, setLoadError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [retryKey, setRetryKey] = useState(0);

  const themeConfig = useMemo(() => ({
    token: getAntDesignTokens(),
    components: getAntDesignComponents(),
  }), []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    setPageComponent(null);

    loadPageComponent(remoteName, entry, remoteEntryUrl)
      .then((mod) => {
        if (!cancelled) {
          setPageComponent(() => mod.default);
          setLoading(false);
        }
      })
      .catch((err: Error) => {
        if (!cancelled) {
          setLoadError(err);
          setLoading(false);
        }
      });

    return () => { cancelled = true; };
  }, [remoteName, entry, retryKey]);

  const handleRetry = useCallback(() => {
    setRetryKey((k) => k + 1);
  }, []);

  if (loading) {
    return (
      <div
        role="status"
        aria-label={`Loading ${pluginId} page`}
        style={{ padding: 24, maxWidth: 1440, width: '100%', margin: '0 auto', boxSizing: 'border-box' }}
      >
        <div style={{ marginBottom: 24 }}>
          <UISkeleton active title lines={2} />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 16, marginBottom: 16 }}>
          {[0, 1, 2].map((item) => (
            <UICard key={item}>
              <UISkeleton active title={false} lines={2} />
            </UICard>
          ))}
        </div>
        <UICard>
          <UISkeleton active title lines={7} />
        </UICard>
      </div>
    );
  }

  if (loadError) {
    const mfError = loadError as PageLoadError;
    return (
      <PluginErrorUI
        type="load"
        pluginId={pluginId}
        pageId={pageId}
        error={loadError}
        remoteName={mfError.remoteName ?? remoteName}
        entry={mfError.exposedModule ?? entry}
        onRetry={handleRetry}
      />
    );
  }

  if (!PageComponent) { return null; }

  const pageContext: PageContext = { pageId, pluginId, config, permissions };

  return (
    <PageErrorBoundary pageId={pageId} pluginId={pluginId} key={retryKey}>
      <ConfigProvider theme={themeConfig}>
        <EventBusProvider>
          <PageContextProvider value={pageContext}>
            <PageComponent />
          </PageContextProvider>
        </EventBusProvider>
      </ConfigProvider>
    </PageErrorBoundary>
  );
}
