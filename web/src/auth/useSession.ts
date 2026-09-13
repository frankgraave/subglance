import { useCallback, useEffect, useState } from "react";
import { onUnauthorized } from "../api/http";
import { fetchCurrentUser, fetchSetupStatus, logout as postLogout } from "./api";
import type { User } from "./api";
import type { Session } from "./session";

/**
 * Resolves, and then owns, the session.
 *
 * The order of the two requests is the whole design. Asking `/auth/me` first
 * is one request for the common case (a returning user with a cookie) and two
 * for a first run; asking `/setup` first is two requests for everyone. The
 * instance is set up exactly once and read by every visitor forever after, so
 * the common case is what the extra round trip should be spent avoiding.
 *
 * A 401 from anywhere in the app lands here through `onUnauthorized`, which is
 * why the session cannot live inside the screen that happened to ask: when a
 * cookie expires mid-session, the request that discovers it is usually a
 * background refetch on a screen nobody is looking at.
 */

/**
 * Where an unfinished sign-out is remembered between page loads.
 *
 * Signing out changes the screen before the server has answered, which is the
 * right trade for a slow server but leaves a real hole when the request never
 * lands at all: the cookie is HttpOnly and still valid, so the next reload
 * discovers it through `/auth/me` and signs the browser straight back in —
 * the session outlives the sign-out (CWE-613). Writing the intent down makes
 * it survive the reload too, so it can be retried before anything is
 * discovered.
 */
const PENDING_LOGOUT_KEY = "subglance.pending-logout";

/*
 * Every access is guarded: `localStorage` throws on access in a browser with
 * cookies blocked, and a sign-out that crashes the app is worse than one that
 * cannot be remembered.
 */
function readPendingLogout(): boolean {
  try {
    return window.localStorage.getItem(PENDING_LOGOUT_KEY) !== null;
  } catch {
    return false;
  }
}

function writePendingLogout(pending: boolean): void {
  try {
    if (pending) window.localStorage.setItem(PENDING_LOGOUT_KEY, "1");
    else window.localStorage.removeItem(PENDING_LOGOUT_KEY);
  } catch {
    // Nothing to fall back to; the in-memory session is still anonymous.
  }
}
export type UseSession = {
  session: Session;
  /** Adopts the user a completed login or setup returned. */
  onSignedIn: (user: User) => void;
  /** Ends the session and returns to the login screen. */
  signOut: () => void;
  /** Re-runs the resolution, for the retry button on the error screen. */
  refresh: () => void;
};

export function useSession(): UseSession {
  const [session, setSession] = useState<Session>({ state: "unknown" });
  /*
   * Bumped by `refresh`, and the only dependency of the effect below.
   *
   * Retrying by calling the fetchers directly from the button would leave two
   * resolutions in flight with no ordering between them, and the slower one
   * would win. Re-running the effect instead aborts the first through its own
   * cleanup, which is the behaviour an abort controller is for.
   */
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    void (async () => {
      try {
        /*
         * An unfinished sign-out is settled before anything is discovered.
         *
         * Order matters: asking `/auth/me` first would hand back the very
         * session the user asked to end, and the screen would restore itself
         * behind their back. If the retry fails again the cookie must be
         * assumed alive, so this stops at an error screen — whose retry
         * button runs this same effect — rather than quietly signing in.
         */
        if (readPendingLogout()) {
          try {
            await postLogout();
            writePendingLogout(false);
          } catch {
            if (controller.signal.aborted) return;
            setSession({
              state: "error",
              message: "signing out could not be confirmed by the server; this session may still be open",
            });
            return;
          }
        }

        const user = await fetchCurrentUser(controller.signal);
        if (controller.signal.aborted) return;
        if (user !== null) {
          setSession({ state: "signedIn", user });
          return;
        }
        const setupRequired = await fetchSetupStatus(controller.signal);
        if (controller.signal.aborted) return;
        setSession(setupRequired ? { state: "setup" } : { state: "anonymous" });
      } catch (error) {
        if (controller.signal.aborted) return;
        setSession({
          state: "error",
          message: error instanceof Error ? error.message : "the server could not be reached",
        });
      }
    })();

    return () => controller.abort();
  }, [attempt]);

  const onSignedIn = useCallback((user: User) => {
    setSession({ state: "signedIn", user });
  }, []);

  const signOut = useCallback(() => {
    /*
     * The screen changes immediately and the request follows.
     *
     * Waiting for the 204 before switching screens means a slow or dead
     * server leaves a signed-out user staring at a dashboard, and the local
     * intent — "I am done here" — is honoured either way: the cookie is
     * HttpOnly, so a failed request leaves the session alive on the server —
     * which is exactly why the intent is written down first and retried on
     * the next load instead of being dropped here.
     */
    setSession({ state: "anonymous" });
    writePendingLogout(true);
    void postLogout()
      .then(() => writePendingLogout(false))
      .catch(() => {
        // Left pending on purpose: the next resolution retries it.
      });
  }, []);

  const refresh = useCallback(() => {
    setSession({ state: "unknown" });
    setAttempt((n) => n + 1);
  }, []);

  /*
   * A rejected credential anywhere in the app ends the session here.
   *
   * Guarded on the current state: a signed-out browser polling a public
   * endpoint would otherwise re-announce "anonymous" on every 401 and reset
   * a login form the user was halfway through typing into.
   */
  useEffect(
    () =>
      onUnauthorized(() => {
        setSession((current) => (current.state === "signedIn" ? { state: "anonymous" } : current));
      }),
    [],
  );

  return { session, onSignedIn, signOut, refresh };
}
