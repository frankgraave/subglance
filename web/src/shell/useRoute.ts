import { useCallback, useEffect, useRef, useState } from "react";
import { confirmLeave } from "./leaveGuard";
import { originFrom, readOrigin } from "./listOrigin";
import type { ListOrigin } from "./listOrigin";
import { parseRoute, routePath } from "./route";
import type { Route } from "./route";

export type UseRoute = {
  route: Route;
  /** Pushes a new entry, so Back returns to where the user was. */
  navigate: (next: Route) => void;
  /** The list a monitor's page was opened from; null anywhere else. */
  origin: ListOrigin | null;
  /**
   * The monitor page's back control: returns to `origin` by traversing
   * history, or opens `/monitors` when there is no list to return to.
   */
  backToList: () => void;
};

/** The key of an entry's position in this tab's history, on `history.state`. */
export const ROUTE_INDEX = "subglanceRouteIndex";
const INDEX = ROUTE_INDEX;
const ORIGIN = "subglanceListOrigin";
function entryIndex(history: History): number | undefined {
  const value = history.state?.[INDEX];
  return Number.isInteger(value) ? value : undefined;
}

/**
 * The current entry's origin, kept only while it can still be returned to:
 * an index at or past the entry itself names no list behind it.
 */
function entryOrigin(history: History, index: number): ListOrigin | null {
  const origin = readOrigin(history.state?.[ORIGIN]);
  return origin !== null && origin.index < index ? origin : null;
}

/**
 * One navigation seam for links and browser Back/Forward. History entries carry
 * an index, not any form data. A cancelled pop is reversed with history.go(),
 * keeping the form mounted AND the forward stack intact rather than replacing
 * the destination with a duplicate of the page we refused to leave.
 */
export function useRoute(history: History = window.history): UseRoute {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));
  const [origin, setOrigin] = useState<ListOrigin | null>(() =>
    entryOrigin(history, entryIndex(history) ?? 0),
  );
  // Read by the popstate handler, which is registered once and so cannot
  // close over the state above.
  const originRef = useRef(origin);
  useEffect(() => {
    originRef.current = origin;
  }, [origin]);
  const current = useRef({ index: 0, url: window.location.href });
  const restoring = useRef(false);

  useEffect(() => {
    const index = entryIndex(history) ?? 0;
    history.replaceState({ ...history.state, [INDEX]: index }, "", window.location.href);
    current.current = { index, url: window.location.href };
    // Re-read the external URL in case browser restoration moved it after render.
    const syncLocation = () => {
      setRoute(parseRoute(window.location.pathname));
      setOrigin(entryOrigin(history, index));
    };
    syncLocation();
    const onPop = () => {
      let nextIndex = entryIndex(history);
      if (restoring.current) {
        // Another Back/Forward can arrive before our reversal. Only finish at
        // the entry we kept mounted, never just at the next popstate.
        if (nextIndex === current.current.index && window.location.href === current.current.url) {
          restoring.current = false;
        } else if (nextIndex !== undefined && nextIndex !== current.current.index) {
          history.go(current.current.index - nextIndex);
        }
        return;
      }
      const nextRoute = parseRoute(window.location.pathname);
      const previousRoute = parseRoute(new URL(current.current.url).pathname);
      if (nextIndex === undefined && routePath(nextRoute) === routePath(previousRoute)) {
        // Native fragment navigation (including the skip link) creates a real
        // entry with null state. Count it too, or a later multi-entry Back is
        // reversed by the wrong distance. Tag in place; do not cut off Forward.
        nextIndex = current.current.index + 1;
        // The same page, so the same way back: without the origin, using the
        // skip link on a monitor's page would turn "← Dashboard" into
        // "← All monitors" for the keyboard users who use it.
        const kept = originRef.current === null ? {} : { [ORIGIN]: originRef.current };
        history.replaceState({ ...history.state, [INDEX]: nextIndex, ...kept }, "", window.location.href);
      }
      if (routePath(nextRoute) !== routePath(previousRoute) && !confirmLeave()) {
        if (nextIndex !== undefined && nextIndex !== current.current.index) {
          restoring.current = true;
          history.go(current.current.index - nextIndex);
        } else {
          // An entry created outside this router has no reversible index. Keep
          // the draft and restore its URL; normal app history uses go() above.
          history.pushState({ [INDEX]: current.current.index }, "", current.current.url);
        }
        return;
      }
      current.current = { index: nextIndex ?? current.current.index, url: window.location.href };
      setRoute(parseRoute(window.location.pathname));
      setOrigin(entryOrigin(history, current.current.index));
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [history]);

  const navigate = useCallback((next: Route) => {
    if (restoring.current) return;
    const path = routePath(next);
    // location changes before popstate is handled; it is not necessarily the
    // screen whose draft we are about to unmount.
    if (path !== routePath(route) && !confirmLeave()) return;
    if (path !== window.location.pathname) {
      const index = current.current.index + 1;
      // Only a monitor's page records where it was opened from; every other
      // entry is written exactly as it was before origins existed.
      const from =
        next.name === "monitor"
          ? originFrom(route, current.current.index, origin)
          : null;
      history.pushState(
        from === null ? { [INDEX]: index } : { [INDEX]: index, [ORIGIN]: from },
        "",
        path,
      );
      current.current = { index, url: window.location.href };
      setOrigin(from);
    }
    setRoute(next);
  }, [history, route, origin]);

  /*
   * Back to the list by traversal, not by a new entry. The list's entry is
   * still in this tab's history and carries the view it was left in (see
   * `entryView.ts`), and Back from there goes where it went before the
   * monitor was opened rather than to the monitor again. The popstate
   * handler above does the rest — including the unsaved-draft check, which a
   * traversal passes through like any other.
   */
  const backToList = useCallback(() => {
    if (restoring.current) return;
    if (origin !== null && origin.index < current.current.index) {
      history.go(origin.index - current.current.index);
      return;
    }
    navigate({ name: "monitors", create: false });
  }, [history, origin, navigate]);

  return { route, navigate, origin, backToList };
}
