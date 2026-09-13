/**
 * The session endpoints, and the password rule the form has to agree with.
 *
 * Kept apart from the screens so a test can drive login and setup without a
 * network, and so the one rule that exists in two languages — how short a
 * password may be — is written down once on this side of the wire.
 */

import { ApiError, apiJSON, apiPost } from "../api/http";

export { ApiError } from "../api/http";

export type User = {
  id: number;
  email: string;
  role: string;
  created_at: string;
};

/**
 * The shortest password the server accepts, mirroring `auth.MinPasswordLength`.
 *
 * A second copy of a rule is a liability, so this one earns its place by being
 * the *only* thing the form decides for itself: it disables the submit button
 * and draws the strength meter, which both have to happen before a request is
 * made. Everything else — what is too long, what is too common — stays the
 * server's answer and is rendered from its message. The guard test in
 * `auth/password.test.ts` fails if the Go constant and this number drift.
 */
export const MIN_PASSWORD_LENGTH = 12;

/** Whether this instance still needs its first account. */
export async function fetchSetupStatus(signal?: AbortSignal): Promise<boolean> {
  const body = await apiJSON<{ setup_required?: boolean }>("/api/v1/setup", { signal });
  return body.setup_required === true;
}

/**
 * Who the session belongs to, or null when there is no session.
 *
 * A 401 here is the expected answer for a signed-out browser, not a failure,
 * so it resolves to null rather than throwing. Every other status still
 * throws: "the server is broken" and "you are signed out" must not look the
 * same to the caller, or an outage would silently present a login form and
 * invite the user to type their password at a server that cannot check it.
 */
export async function fetchCurrentUser(signal?: AbortSignal): Promise<User | null> {
  try {
    return await apiJSON<User>("/api/v1/auth/me", { signal });
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) return null;
    throw error;
  }
}

/** Creates the first administrator. The response is already signed in. */
export async function createFirstUser(email: string, password: string): Promise<User> {
  const res = await apiPost("/api/v1/setup", { email, password });
  return (await res.json()) as User;
}

/** Exchanges credentials for a session cookie. */
export async function login(email: string, password: string): Promise<User> {
  const res = await apiPost("/api/v1/auth/login", { email, password });
  return (await res.json()) as User;
}

/**
 * Ends the session.
 *
 * The endpoint is public and returns 204 even without a cookie, which is what
 * makes it safe to call from a session that has already expired: it is never
 * possible to be stuck holding a stale cookie you cannot clear.
 */
export async function logout(): Promise<void> {
  await apiPost("/api/v1/auth/logout", {});
}
