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
  | { name: "incidents" }
  /**
   * The monitors inventory. `create` is part of the route rather than a
   * component flag because `/monitors/new` has to be a real address: the
   * empty state is the onboarding, and "here is the form" is the single most
   * pasted link a self-hoster sends a colleague.
   */
  | { name: "monitors"; create: boolean }
  | { name: "monitor"; id: string };

export const DASHBOARD_PATH = "/";

/**
 * The incidents screen.
 *
 * A place, like a monitor, and for a stronger reason than the monitor was: it
 * is the screen somebody is sent to at 03:00, usually by a person pasting a
 * link into a chat window. A boolean cannot be pasted.
 */
export const INCIDENTS_PATH = "/incidents";

/** The monitors inventory: what is configured, and where it is changed. */
export const MONITORS_PATH = "/monitors";

/**
 * The inventory with the create drawer open.
 *
 * A child of `/monitors` rather than a `?new` query, because it is a place you
 * can be sent to and the list behind it is part of what you were sent to see —
 * the ticket's argument for a drawer over a page is that you add a monitor
 * *against* the inventory.
 */
export const MONITOR_CREATE_PATH = "/monitors/new";

/**
 * The id segment `/monitors/new` occupies, and therefore the one id that can
 * never address a monitor.
 *
 * Server ids are int64s rendered as digits, so nothing real can collide — but
 * the reservation is written down rather than assumed, because `parseRoute`
 * has to make the choice explicitly and a reader has to be able to see which
 * way it went.
 */
export const CREATE_SEGMENT = "new";

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
  if (segments.length === 1 && segments[0] === "incidents") {
    return { name: "incidents" };
  }
  if (segments.length === 1 && segments[0] === "monitors") {
    return { name: "monitors", create: false };
  }
  if (segments.length === 2 && segments[0] === "monitors") {
    /*
     * `new` is the create address, not a monitor id. It is checked before the
     * decode so that `/monitors/new` cannot be reached twice by two spellings:
     * `%6eew` decodes to `new` and would otherwise open the form at a URL that
     * `routePath` can never produce, leaving the address bar disagreeing with
     * the screen. An id is what the server issued, and the server issues
     * digits.
     */
    if (segments[1] === CREATE_SEGMENT) {
      return { name: "monitors", create: true };
    }
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
  if (route.name === "monitor") return monitorPath(route.id);
  if (route.name === "monitors")
    return route.create ? MONITOR_CREATE_PATH : MONITORS_PATH;
  if (route.name === "incidents") return INCIDENTS_PATH;
  return DASHBOARD_PATH;
}
