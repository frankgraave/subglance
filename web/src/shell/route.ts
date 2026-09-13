/**
 * The URL is the screen you are on.
 *
 * `App` used to switch screens with booleans. That is fine while every screen
 * is a mode of one page, but a monitor detail view is a *place*: an alert has
 * to be able to link to it, and a person has to be able to send it to someone
 * else. A boolean cannot be pasted into a chat window.
 *
 * Parsing lives here, apart from React, because it is a pure string function
 * and deserves a test that does not need a renderer or a history stub.
 */

export type Route =
  | { name: "dashboard" }
  | { name: "monitor"; id: string };

export const DASHBOARD_PATH = "/";

/** The canonical path for one monitor. The only place this shape is written. */
export function monitorPath(id: string): string {
  return `/monitors/${encodeURIComponent(id)}`;
}

/**
 * Reads a route out of a pathname.
 *
 * Anything unrecognised is the dashboard rather than a 404 screen. The server
 * already hands the SPA shell to every non-API path it does not know, so a
 * typo'd URL arrives here; showing the dashboard is both the honest answer
 * ("that page does not exist, here is the one that does") and the one that
 * leaves the user somewhere useful.
 */
export function parseRoute(pathname: string): Route {
  const segments = pathname.split("/").filter((segment) => segment !== "");
  if (segments.length === 2 && segments[0] === "monitors") {
    /*
     * Decoded, because `monitorPath` encoded it. Ids are digits today, but
     * round-tripping through the encoder is what keeps this correct if they
     * ever stop being — and `decodeURIComponent` throws on a malformed
     * escape such as `%zz`, which a hand-typed URL can easily contain.
     */
    let id: string;
    try {
      id = decodeURIComponent(segments[1]);
    } catch {
      return { name: "dashboard" };
    }
    if (id === "") return { name: "dashboard" };
    return { name: "monitor", id };
  }
  return { name: "dashboard" };
}

/** The path a route lives at. Inverse of `parseRoute` for known routes. */
export function routePath(route: Route): string {
  return route.name === "monitor" ? monitorPath(route.id) : DASHBOARD_PATH;
}
