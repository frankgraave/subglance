import { AuthScreen } from "./AuthScreen";
import type { User } from "./api";
import type { Session } from "./session";

/**
 * Decides what the whole app is, given the session.
 *
 * A separate component from `App` so the branch can be tested by handing it a
 * session rather than by stubbing four endpoints, and so `App` keeps holding
 * only the things a signed-in user's screen needs.
 *
 * The `unknown` state deliberately renders almost nothing. A skeleton
 * dashboard would be a guess about which screen is coming, and guessing wrong
 * means a stranger's first frame is a fake dashboard that then vanishes; the
 * resolution is one request against a local server, so there is very little
 * time to fill and nothing worth filling it with.
 */

export type SessionGateProps = {
  session: Session;
  onSignedIn: (user: User) => void;
  onRetry: () => void;
  /** The app itself, rendered only once there is a session. */
  children: React.ReactNode;
};

export function SessionGate({
  session,
  onSignedIn,
  onRetry,
  children,
}: SessionGateProps) {
  switch (session.state) {
    case "unknown":
      return (
        <div className="auth-screen">
          {/*
           * Announced rather than silent: a screen reader on a slow
           * connection would otherwise be told nothing at all between the
           * page loading and the login form appearing.
           */}
          <p className="auth-waiting" role="status">
            Checking your session…
          </p>
        </div>
      );

    case "error":
      return (
        <main className="auth-screen">
          <div className="auth-card">
            <h1 className="auth-title">Cannot reach this instance</h1>
            {/*
             * The login form is deliberately not shown here. Presenting one
             * when the server is unreachable invites someone to type their
             * password into a page that cannot check it, and then reports
             * their correct password as a failure.
             */}
            <p className="auth-intro">
              SubGlance is running in your browser, but the server behind it did
              not answer, so there is no way to tell whether you are signed in.
            </p>
            <p className="auth-error" role="alert">
              {session.message}
            </p>
            <button className="auth-submit" type="button" onClick={onRetry}>
              Try again
            </button>
          </div>
        </main>
      );

    case "setup":
      return <AuthScreen mode="setup" onSignedIn={onSignedIn} />;

    case "anonymous":
      return <AuthScreen mode="login" onSignedIn={onSignedIn} />;

    case "signedIn":
      return <>{children}</>;
  }
}
