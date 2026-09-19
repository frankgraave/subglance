import { useCallback, useEffect, useRef, useState } from "react";
import { confirmLeave } from "./leaveGuard";
import { parseRoute, routePath } from "./route";
import type { Route } from "./route";

export type UseRoute = {
  route: Route;
  /** Pushes a new entry, so Back returns to where the user was. */
  navigate: (next: Route) => void;
};

const INDEX = "subglanceRouteIndex";
function entryIndex(history: History): number | undefined {
  const value = history.state?.[INDEX];
  return Number.isInteger(value) ? value : undefined;
}

/**
 * One navigation seam for links and browser Back/Forward. History entries carry
 * an index, not any form data. A cancelled pop is reversed with history.go(),
 * keeping the form mounted AND the forward stack intact rather than replacing
 * the destination with a duplicate of the page we refused to leave.
 */
export function useRoute(history: History = window.history): UseRoute {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));
  const current = useRef({ index: 0, url: window.location.href });
  const restoring = useRef(false);

  useEffect(() => {
    const index = entryIndex(history) ?? 0;
    history.replaceState({ ...history.state, [INDEX]: index }, "", window.location.href);
    current.current = { index, url: window.location.href };
    // Re-read the external URL in case browser restoration moved it after render.
    const syncLocation = () => setRoute(parseRoute(window.location.pathname));
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
        history.replaceState({ ...history.state, [INDEX]: nextIndex }, "", window.location.href);
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
      history.pushState({ [INDEX]: index }, "", path);
      current.current = { index, url: window.location.href };
    }
    setRoute(next);
  }, [history, route]);

  return { route, navigate };
}
