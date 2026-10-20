import { Component, type ReactNode, type ErrorInfo } from 'react';
import { PluginErrorUI } from './plugin-error-ui.js';
import { normalizeStudioFailure, recordStudioFailure, type StudioFailure } from './error-contract.js';

interface Props {
  pageId: string;
  pluginId: string;
  children: ReactNode;
  onError?: (error: Error, errorInfo: ErrorInfo) => void;
}

interface State {
  hasError: boolean;
  error: Error | null;
  failure: StudioFailure | null;
}

/**
 * Error boundary that isolates plugin page runtime crashes.
 * A crashed plugin does NOT bring down the rest of Studio.
 */
export class PageErrorBoundary extends Component<Props, State> {
  override state: State = { hasError: false, error: null, failure: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error, failure: normalizeStudioFailure(error) };
  }

  override componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    const failure = this.state.failure ?? normalizeStudioFailure(error);
    recordStudioFailure(`Plugin page ${this.props.pluginId}/${this.props.pageId} crashed`, failure);
    this.props.onError?.(error, errorInfo);
  }

  handleRetry = (): void => {
    this.setState({ hasError: false, error: null, failure: null });
  };

  override render(): ReactNode {
    if (this.state.hasError && this.state.error) {
      return (
        <PluginErrorUI
          type="crash"
          pluginId={this.props.pluginId}
          pageId={this.props.pageId}
          failure={this.state.failure ?? normalizeStudioFailure(this.state.error)}
          onRetry={this.handleRetry}
        />
      );
    }
    return this.props.children;
  }
}
