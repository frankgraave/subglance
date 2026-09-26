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

/**
 * The expanded sidebar and the collapsed rail, in pixels.
 *
 * Mirrors `--size-sidebar` and `--size-rail` in tokens.css, which is what the
 * shell's grid track actually uses. They are repeated here because the layout
 * veto below is decided in React, and `breakpoint.test.ts` asserts the two
 * files agree so the copy cannot drift from the value.
 */
export const SIDEBAR_WIDTH = 232;
export const RAIL_WIDTH = 56;

/**
 * The widest viewport at which the expanded sidebar still vetoes the rows
 * layout: 816px (SUB-149).
 *
 * The 640px breakpoint is a viewport width, but what decides whether five
 * facts fit on one line is the width of the content column beside the
 * navigation. Above 640px the navigation is always there, and the narrowest
 * column the row layouts were ever designed into is the one beside the
 * *rail*: 641 − 56 = 585. Beside the expanded sidebar the same 641px viewport
 * leaves 176px less, and the rows table scrolled the page 62px sideways.
 *
 * So the veto extends by exactly the difference between the two, and rows
 * come back at the first width where the column beside the sidebar is
 * as wide as the one beside the rail at 641px — no new judgement about how
 * wide a row must be, only the existing one applied to the column instead of
 * the window.
 */
export const SIDEBAR_VETO_MAX_WIDTH = COMPACT_MAX_WIDTH + SIDEBAR_WIDTH - RAIL_WIDTH;

/**
 * True when the expanded sidebar leaves the content column too narrow for the
 * rows table, although the viewport is above the breakpoint.
 *
 * Only the sidebar decides it: with the rail, every width above 640px has the
 * column the row layout was designed into. Both queries are subscribed to on
 * every render because hooks cannot be skipped; the sidebar only decides
 * whether the answer counts.
 */
export function useSidebarSqueeze(sidebarCollapsed: boolean): boolean {
  const narrow = useCompactViewport();
  const besideSidebar = useMediaQuery(`(max-width: ${SIDEBAR_VETO_MAX_WIDTH}px)`);
  return !narrow && !sidebarCollapsed && besideSidebar;
}
