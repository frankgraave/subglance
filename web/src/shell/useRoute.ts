import { useCallback, useEffect, useState } from "react";
import { parseRoute, routePath } from "./route";
import type { Route } from "./route";

/**
 * The current route, and the one way to change it.
 *
 * Deliberately not a router library. The product has two screens and one
 * parameter; a router would add a dependency, a provider and a matching
 * algorithm to answer a question `parseRoute` answers in four lines. If the
 * screen count grows past counting, this hook is the seam to replace.
 *
 * `popstate` is the whole subtlety: `pushState` does not fire it, so the hook
 * sets its own state on navigation *and* listens for the back button. Missing
 * the listener is the classic hand-rolled-router bug — Back changes the URL
 * and nothing on screen moves.
 */

export type UseRoute = {
  route: Route;
  /** Pushes a new entry, so Back returns to where the user was. */
  navigate: (next: Route) => void;
};

export function useRoute(history: History = window.history): UseRoute {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));

  useEffect(() => {
    const onPop = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", onPop);
    /*
     * Re-read on mount as well as on every pop. Between the lazy initialiser
     * above and this effect the URL can already have moved — a browser
     * restoring a session does exactly that — and a stale first render would
     * show the dashboard at a monitor URL.
     */
    onPop();
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback(
    (next: Route) => {
      const path = routePath(next);
      if (path !== window.location.pathname) history.pushState(null, "", path);
      setRoute(next);
    },
    [history],
  );

  return { route, navigate };
}
