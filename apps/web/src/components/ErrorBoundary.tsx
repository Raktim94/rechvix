import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
}
interface State {
  error: Error | null;
}

/** Catches a render-time crash anywhere below it and shows a recoverable
 * screen instead of an unrecovered blank page — before this existed, one
 * bad value reaching e.g. `.length` on a page far from here (a null array
 * in a bulk-delete response, say) took the ENTIRE app down to a white
 * screen with no way back short of a manual reload, since nothing in this
 * tree ever caught a React render error. Wraps the whole app in main.tsx,
 * outside the router, so it survives even a crash inside routing itself. */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Unhandled render error:", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div style={{ maxWidth: 480, margin: "80px auto", padding: 24, textAlign: "center", fontFamily: "sans-serif" }}>
          <h1 style={{ fontSize: 20, marginBottom: 8 }}>Something went wrong</h1>
          <p style={{ color: "#666", marginBottom: 20 }}>
            This page hit an unexpected error. Reloading usually fixes it — your data is safe, this only affected the display.
          </p>
          <button
            type="button"
            onClick={() => {
              this.setState({ error: null });
              window.location.reload();
            }}
            style={{ padding: "8px 20px", borderRadius: 6, border: "none", background: "#2563eb", color: "#fff", cursor: "pointer", fontSize: 14 }}
          >
            Reload
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
