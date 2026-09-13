import { useCallback, useState } from "react";
import { ApiError, createFirstUser, login } from "./api";
import type { User } from "./api";
import { CredentialsForm } from "./CredentialsForm";
import type { Rejection } from "./CredentialsForm";

/**
 * The screen you see when you are not signed in.
 *
 * It is the first thing a stranger sees, so it carries the product mark and a
 * sentence saying what this instance is, rather than being a bare pair of
 * inputs. Nothing about it links anywhere: there is no "forgot password" on a
 * self-hosted instance with no mail server to send from, and an offer that
 * cannot be honoured is worse than its absence (the README documents the
 * recovery path, which needs shell access by design).
 *
 * The data owner for both forms. Everything that touches the network, aborts
 * nothing and turns an HTTP status into a sentence lives here, so
 * `CredentialsForm` can stay a pure function of props.
 */

export type AuthScreenProps = {
  mode: "login" | "setup";
  onSignedIn: (user: User) => void;
  /** Injected in tests. Defaults to the real endpoints. */
  api?: {
    login: typeof login;
    createFirstUser: typeof createFirstUser;
  };
};

export function AuthScreen({ mode, onSignedIn, api }: AuthScreenProps) {
  const signIn = api?.login ?? login;
  const create = api?.createFirstUser ?? createFirstUser;

  const [submitting, setSubmitting] = useState(false);
  const [rejection, setRejection] = useState<Rejection | null>(null);

  const setup = mode === "setup";

  const submit = useCallback(
    (email: string, password: string) => {
      setSubmitting(true);
      setRejection(null);
      void (async () => {
        try {
          const user = setup ? await create(email, password) : await signIn(email, password);
          onSignedIn(user);
        } catch (error) {
          setRejection(explain(error));
          setSubmitting(false);
        }
        /*
         * No `finally`: on success this component is about to be unmounted by
         * the session change, and clearing `submitting` there would be a
         * state update on an unmounted tree. Leaving the button in its
         * "signing in" state until it disappears is also the honest reading —
         * the work did not stop, the screen did.
         */
      })();
    },
    [create, onSignedIn, setup, signIn],
  );

  return (
    <main className="auth-screen">
      <div className="auth-card">
        <div className="auth-brand">
          {/* The same lamp the sidebar draws, by the same class: the
              heartbeat lamp is the product mark, and a second logo here would
              be a second thing to keep in step. Decorative, so it is hidden
              from assistive technology and the name beside it is the mark. */}
          <span className="auth-brand-mark" aria-hidden="true">
            <span className="led" data-state="up" />
          </span>
          <span className="auth-brand-name">SubGlance</span>
        </div>

        <h1 className="auth-title">{setup ? "Set up this instance" : "Sign in"}</h1>
        <p className="auth-intro">
          {setup
            ? "Nobody has an account here yet. The first one you create is the administrator, and this page closes for good once it exists."
            : "This instance is watched by whoever holds an account on it."}
        </p>

        <CredentialsForm
          mode={mode}
          onSubmit={submit}
          submitting={submitting}
          rejection={rejection}
        />

        {setup && (
          <p className="auth-note">
            Credentials are stored on this machine and never sent anywhere else.
          </p>
        )}
      </div>
    </main>
  );
}

/**
 * The sentence to show for a failure, and which input it belongs under.
 *
 * The server's own wording is used wherever there is one: it knows which
 * field it rejected and what the acceptable shape is. Only the cases where
 * the status carries information the body does not are reworded here.
 */
function explain(error: unknown): Rejection {
  if (error instanceof ApiError) {
    if (error.status === 429) {
      const wait = error.retryAfter;
      const minutes = wait === null ? null : Math.max(1, Math.round(wait / 60));
      return {
        message:
          minutes === null
            ? error.message
            : `Too many failed attempts. Try again in about ${minutes} minute${minutes === 1 ? "" : "s"}.`,
      };
    }
    if (error.status === 409) {
      // Setup raced another tab, or the instance was set up while this page
      // sat open. Saying so is more use than the API's terser sentence,
      // because the next action is a reload rather than a retry.
      return {
        message: "This instance already has an account. Reload the page to sign in.",
      };
    }
    return {
      message: error.message,
      ...(error.field !== null ? { field: error.field } : {}),
    };
  }
  // A network failure has no status and no body: fetch rejects outright.
  return {
    message: "Could not reach the server. Check that it is still running, then try again.",
  };
}
