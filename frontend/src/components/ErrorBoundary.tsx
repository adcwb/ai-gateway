// Catches render errors anywhere in the tree so a single bad component
// (see: the earlier Dashboard null-length crash) shows a recoverable state
// instead of blanking the whole app.
import React from "react";
import { Icon } from "./ui";

interface State { error: Error | null; }

export class ErrorBoundary extends React.Component<{ children: React.ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("Unhandled render error:", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="errboundary">
          <div className="box">
            <div style={{ color: "var(--err)", marginBottom: 12 }}>
              <Icon name="alert" size={28} />
            </div>
            <h1>Something broke</h1>
            <p>An unexpected error rendered this view. Your data is safe — reload to continue.</p>
            <pre>{this.state.error.message || String(this.state.error)}</pre>
            <button onClick={() => window.location.reload()}>
              <Icon name="refresh" size={14} /> Reload
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
