/**
 * The last line of defence against a render crash.
 *
 * React 19 unmounts the whole root when a render throws and nothing catches
 * it, so one malformed field in one row takes the entire page down to a white
 * screen. On a wall-mounted monitoring display that is indistinguishable from
 * the machine being switched off, which is the worst possible failure for a
 * tool whose only job is to say whether things are alive.
 *
 * A boundary cannot repair the broken state, so it does not pretend to: it
 * says something broke, offers the two actions that can actually help, and
 * keeps the surrounding chrome on screen wherever it is mounted below the
 * root.
 *
 * A class is not a style choice here. `getDerivedStateFromError` and
 * `componentDidCatch` have no hook equivalent in React 19.
 */

import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";

export type ErrorBoundaryProps = {
  children: ReactNode;
  /** Sentence above the actions. Defaults to the page-level wording. */
  title?: string;
  /**
   * Called when the boundary catches. Injected in tests; defaults to the
   * console so a crash still leaves a trace in a real browser.
   */
  onError?: (error: Error, info: ErrorInfo) => void;
  /** Reloads the document. Injected in tests. */
  onReload?: () => void;
};

type ErrorBoundaryState = { error: Error | null };

export class ErrorBoundary extends Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): ErrorBoundaryState {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    const report = this.props.onError;
    if (report) {
      report(error, info);
      return;
    }
    // Not swallowed. Whoever has the tab open with devtools should still see
    // the stack; the panel is for the person who does not.
    console.error("render error", error, info.componentStack);
  }

  private readonly retry = () => this.setState({ error: null });

  private readonly reload = () => {
    const reload = this.props.onReload;
    if (reload) {
      reload();
      return;
    }
    window.location.reload();
  };

  render() {
    const { error } = this.state;
    if (error === null) return this.props.children;

    const title =
      this.props.title ?? "Something broke while drawing this page.";
    return (
      <section className="crash" role="alert" aria-live="assertive">
        <h2 className="crash-title">{title}</h2>
        {/*
         * The message is shown rather than hidden behind a console. Whoever
         * runs SubGlance runs the server too, so the detail that makes a bug
         * report useful belongs on screen, not only in devtools.
         */}
        <p className="crash-detail">{error.message}</p>
        <div className="crash-actions">
          <button type="button" className="crash-button" onClick={this.retry}>
            Try again
          </button>
          <button type="button" className="crash-button" onClick={this.reload}>
            Reload the page
          </button>
        </div>
      </section>
    );
  }
}
