import { Component, type ErrorInfo, type ReactNode } from 'react';
import { ErrorState } from '@/components/page-state';

interface Props {
  children: ReactNode;
  /** Label for the failing region, shown in the message. */
  label: string;
}

interface State {
  error?: Error;
}

/**
 * Contains a render failure to one region, so a broken panel (for example a
 * replay transcript the runtime rejects) shows a message instead of a blank page.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error(`[dashboard] ${this.props.label} failed to render`, error, info.componentStack);
  }

  render(): ReactNode {
    if (this.state.error) {
      return <ErrorState error={new Error(`${this.props.label} failed to render: ${this.state.error.message}`)} />;
    }
    return this.props.children;
  }
}
