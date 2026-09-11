import { useCallback, useSyncExternalStore } from "react";

/**
 * Subscribes to a CSS media query from React.
 *
 * `useSyncExternalStore` rather than `useState` + `useEffect`: the match is an
 * external, already-existing value, not state we own. Reading it in the
 * snapshot means the first render is already correct instead of rendering the
 * wrong layout and correcting it after paint, and it keeps the component free
 * of a `setState` inside an effect (which the linter rejects, for good
 * reason).
 *
 * The server snapshot is deliberately `false`: without a viewport there is no
 * honest answer, and the desktop layout is the one that degrades gracefully
 * when it turns out to be wrong. jsdom has no `matchMedia` at all, so the same
 * fallback keeps tests rendering the table unless they ask otherwise.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
        return () => {};
      }
      const list = window.matchMedia(query);
      // addEventListener has been the supported API for years; addListener is
      // only kept for engines that never got it.
      if (typeof list.addEventListener === "function") {
        list.addEventListener("change", onChange);
        return () => list.removeEventListener("change", onChange);
      }
      list.addListener(onChange);
      return () => list.removeListener(onChange);
    },
    [query],
  );

  const snapshot = useCallback(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
    return window.matchMedia(query).matches;
  }, [query]);

  return useSyncExternalStore(subscribe, snapshot, () => false);
}

/**
 * The one breakpoint the dashboard has, in pixels.
 *
 * 640px, and it is a layout decision rather than a device one: below it the
 * five-column row cannot hold a readable name, a heartbeat and two numbers at
 * once (DESIGN.md §12). Exported so the component and its tests name the same
 * number, and so the value can be asserted against the CSS.
 */
export const COMPACT_MAX_WIDTH = 640;

/** True when the viewport is too narrow for the row layout. */
export function useCompactViewport(): boolean {
  return useMediaQuery(`(max-width: ${COMPACT_MAX_WIDTH}px)`);
}
