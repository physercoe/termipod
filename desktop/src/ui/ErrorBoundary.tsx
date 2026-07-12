import { Component, type ErrorInfo, type ReactNode } from 'react';

/// A surface-level error boundary. Before this existed, any render-time throw in a
/// surface (e.g. the `ref`-reserved-prop bug in ReadSurface) unmounted the whole
/// React tree — the app went blank and the only recovery was relaunching the
/// desktop app. Wrapping the surface switch keeps the shell chrome (tabs, status
/// bar) alive so the user can navigate away or reload in-app instead.
///
/// Keyed remount: give it `key={activeSurface}` so switching surfaces resets a
/// crashed boundary automatically.

interface Props {
  children: ReactNode;
  /// Shown in the panel header so the user knows which surface failed.
  label?: string;
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // The webview console is the only sink we have on-device; log both so a
    // director looking at the devtools sees the component stack too.
    // eslint-disable-next-line no-console
    console.error('[surface-crash]', this.props.label ?? '', error, info.componentStack);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (error === null) return this.props.children;
    return (
      <div className="surface-crash region-pad">
        <div className="surface-crash-card">
          <div className="surface-crash-title">
            {this.props.label !== undefined ? `${this.props.label} crashed` : 'This view crashed'}
          </div>
          <div className="surface-crash-msg mono small">{error.message}</div>
          <div className="surface-crash-actions">
            <button className="primary small" onClick={() => this.setState({ error: null })}>
              Try again
            </button>
            <button className="small" onClick={() => window.location.reload()}>
              Reload app
            </button>
          </div>
          <div className="muted small">
            The rest of the app is still running — you can switch to another tab.
          </div>
        </div>
      </div>
    );
  }
}
