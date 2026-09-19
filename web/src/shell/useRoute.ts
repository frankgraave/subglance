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
      if (restoring.current) { restoring.current = false; return; }
      const nextIndex = entryIndex(history);
      const nextRoute = parseRoute(window.location.pathname);
      const previousRoute = parseRoute(new URL(current.current.url).pathname);
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
    if (path !== window.location.pathname) {
      if (!confirmLeave()) return;
      const index = current.current.index + 1;
      history.pushState({ [INDEX]: index }, "", path);
      current.current = { index, url: window.location.href };
    }
    setRoute(next);
  }, [history]);

  return { route, navigate };
}
