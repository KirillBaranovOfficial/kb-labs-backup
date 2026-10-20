/** @module @kb-labs/studio-app/components/widget-error-boundary */

import * as React from 'react';
import { UIButton, UITypographyText, UITag } from '@kb-labs/studio-ui-kit';
import { AlertTriangle, RotateCcw } from 'lucide-react';
import { normalizeStudioFailure, recordStudioFailure, type StudioFailure } from '@kb-labs/studio-federation';
import { trackWidgetEvent } from '../utils/analytics';

export interface WidgetErrorBoundaryProps {
  widgetId?: string;
  pluginId?: string;
  children: React.ReactNode;
}

interface State {
  hasError: boolean;
  failure: StudioFailure | null;
  copied: boolean;
}

export class WidgetErrorBoundary extends React.Component<WidgetErrorBoundaryProps, State> {
  constructor(props: WidgetErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, failure: null, copied: false };
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { hasError: true, failure: normalizeStudioFailure(error) };
  }

  override componentDidCatch(_error: Error, _errorInfo: React.ErrorInfo): void {
    const { failure } = this.state;
    if (!failure) {
      return;
    }
    if (this.props.widgetId && this.props.pluginId) {
      trackWidgetEvent('error', {
        widgetId: this.props.widgetId,
        pluginId: this.props.pluginId,
        code: failure.code,
      });
    }
    recordStudioFailure('Studio widget failed', failure);
  }

  resetError = (): void => {
    this.setState({ hasError: false, failure: null, copied: false });
  };

  copyDiagnostics = (): void => {
    const { failure } = this.state;
    if (!failure) {
      return;
    }
    navigator.clipboard.writeText(`${failure.code}\n${failure.correlationId}`).then(() => {
      this.setState({ copied: true });
    });
  };

  override render(): React.ReactNode {
    const { hasError, failure, copied } = this.state;
    if (!hasError || !failure) {
      return this.props.children;
    }

    const { widgetId, pluginId } = this.props;
    return (
      <div style={{
        height: '100%',
        minHeight: 120,
        border: '1px solid var(--border-primary)',
        borderRadius: 8,
        display: 'flex',
        flexDirection: 'column',
        padding: '12px 14px',
        gap: 6,
        backgroundColor: 'var(--bg-secondary)',
        overflow: 'hidden',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <AlertTriangle size={14} style={{ color: 'var(--error)', flexShrink: 0 }} />
          <UITypographyText strong style={{ fontSize: 13 }}>Widget failed</UITypographyText>
        </div>
        {(pluginId || widgetId) && (
          <UITypographyText type="secondary" style={{ fontSize: 11, fontFamily: 'monospace' }}>
            {[pluginId, widgetId].filter(Boolean).join(' › ')}
          </UITypographyText>
        )}
        <UITypographyText type="secondary" style={{ fontSize: 12 }}>{failure.message}</UITypographyText>
        <UITypographyText type="secondary" style={{ fontSize: 11 }}>{failure.hint}</UITypographyText>
        <div style={{ display: 'flex', gap: 6, marginTop: 2 }}>
          <UIButton size="small" icon={<RotateCcw size={11} />} onClick={this.resetError}>Retry</UIButton>
          <UIButton size="small" onClick={this.copyDiagnostics}>{copied ? 'Copied' : 'Copy diagnostics'}</UIButton>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <UITag variant="error" style={{ margin: 0, fontSize: 10 }}>{failure.code}</UITag>
          <UITypographyText type="secondary" style={{ fontSize: 11 }}>
            Correlation ID: {failure.correlationId}
          </UITypographyText>
        </div>
      </div>
    );
  }
}
