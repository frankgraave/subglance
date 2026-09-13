/**
 * What the app knows about who is signed in.
 *
 * A union rather than a user plus two booleans, because the states are not
 * independent: "still asking" and "nobody" render completely different
 * screens, and a boolean pair makes the illegal fourth combination
 * representable. The first paint of a signed-in instance must not be the
 * login form, so `unknown` is a real state with its own screen rather than
 * being folded into "signed out".
 */

import type { User } from "./api";

export type Session =
  | { state: "unknown" }
  /** No account exists yet: the instance has never been set up. */
  | { state: "setup" }
  | { state: "anonymous" }
  | { state: "signedIn"; user: User }
  /**
   * The status could not be determined at all — the server is down or the
   * browser is offline. Distinct from `anonymous` on purpose: presenting a
   * login form to someone whose server is unreachable invites them to type a
   * password into something that cannot check it, and then blames them for
   * the failure.
   */
  | { state: "error"; message: string };
