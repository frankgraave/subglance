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
  | { name: "settings" }
  /**
   * The monitors inventory. `create` is part of the route rather than a
   * component flag because `/monitors/new` has to be a real address: the
   * empty state is the onboarding, and "here is the form" is the single most
   * pasted link a self-hoster sends a colleague.
   */
  | { name: "monitors"; create: boolean }
  /**
   * The notification channels. `create` is part of the route for the same
   * reason it is on `/monitors`: `/notifications/new` has to be a real
   * address, because "alerts are going nowhere" is the state a fresh install
   * is in and "here is the form that fixes it" is the link somebody sends.
   */
  | { name: "notifications"; create: boolean }
  | { name: "monitor"; id: string }
  /**
   * The component workbench: every component in every state, on fixtures
   * (SUB-193). A developer tool, so it is an address and not a control —
   * nothing in the product links to it, and a person who never types it
   * never meets fixture data that looks like their own.
   */
  | { name: "workbench" }
  /**
   * An address that names no screen. It carries the path it was reached at,
   * so `routePath` stays the inverse of `parseRoute`: the address bar keeps
   * what was typed, and two different wrong addresses stay two different
   * places to the focus and history logic, which compare paths.
   */
  | { name: "notFound"; path: string };

export const DASHBOARD_PATH = "/";

/**
 * The incidents screen.
 *
 * A place, like a monitor, and for a stronger reason than the monitor was: it
 * is the screen somebody is sent to at 03:00, usually by a person pasting a
 * link into a chat window. A boolean cannot be pasted.
 */
export const INCIDENTS_PATH = "/incidents";

/** Account settings, beginning with the password card. */
export const SETTINGS_PATH = "/settings";

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

/** The component workbench. Typed, never linked. */
export const WORKBENCH_PATH = "/workbench";

/** The notification channels: who hears about an incident, and whether they can. */
export const NOTIFICATIONS_PATH = "/notifications";

/** The channel list with the add drawer open. */
export const NOTIFICATION_CREATE_PATH = "/notifications/new";

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
 * Anything unrecognised is the not-found screen. The server hands the SPA
 * shell to every non-API path it does not know, so a typo'd or stale URL
 * arrives here. Drawing the dashboard for it, as this function used to, left
 * the wrong address in the bar under a tab that said "Dashboard": the link
 * looked as though it worked, so nobody corrected it, and whoever followed it
 * never learned that the screen it promised was not there. The not-found
 * screen says so and links to the dashboard, so the user is still one press
 * from somewhere useful.
 */
export function parseRoute(pathname: string): Route {
  const segments = pathname.split("/").filter((segment) => segment !== "");
  const notFound: Route = { name: "notFound", path: pathname };
  if (segments.length === 0) return { name: "dashboard" };
  if (segments.length === 1 && segments[0] === "settings") return { name: "settings" };
  if (segments.length === 1 && segments[0] === "workbench") return { name: "workbench" };
  if (segments.length === 1 && segments[0] === "incidents") {
    return { name: "incidents" };
  }
  if (segments.length === 1 && segments[0] === "monitors") {
    return { name: "monitors", create: false };
  }
  if (segments.length === 1 && segments[0] === "notifications") {
    return { name: "notifications", create: false };
  }
  /*
   * `/notifications/new` opens the add drawer, and nothing else under
   * `/notifications` is a place. There is no per-channel route — a channel has
   * no detail view — so a deeper path is not found, rather than quietly
   * rendering the list for an address that promises one channel.
   */
  if (
    segments.length === 2 &&
    segments[0] === "notifications" &&
    segments[1] === CREATE_SEGMENT
  ) {
    return { name: "notifications", create: true };
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
      return notFound;
    }
    if (id === "") return notFound;
    return { name: "monitor", id };
  }
  return notFound;
}

/** The path a route lives at. Inverse of `parseRoute` for known routes. */
export function routePath(route: Route): string {
  if (route.name === "monitor") return monitorPath(route.id);
  if (route.name === "monitors")
    return route.create ? MONITOR_CREATE_PATH : MONITORS_PATH;
  if (route.name === "notifications")
    return route.create ? NOTIFICATION_CREATE_PATH : NOTIFICATIONS_PATH;
  if (route.name === "incidents") return INCIDENTS_PATH;
  if (route.name === "settings") return SETTINGS_PATH;
  if (route.name === "workbench") return WORKBENCH_PATH;
  if (route.name === "notFound") return route.path;
  return DASHBOARD_PATH;
}
