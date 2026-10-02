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
 * What the rows table needs beside the navigation, in pixels (SUB-194).
 *
 * The table's four fixed columns are status 64, last checks 168, latency 104
 * and 24h 104: 440px before the name gets anything. The name's floor is
 * rung 4 (168px, the inventory's floor too) plus the cell's 24px of padding,
 * and around the table sit the page's 48px of padding and the card's 14px of
 * padding and border. `layout/dashboard-names.browser.test.ts` measures the
 * name cell at the first width where rows come back, so these numbers cannot
 * drift from the CSS unnoticed.
 */
export const ROWS_FIXED_COLUMNS = 440;
export const ROWS_NAME_FLOOR = 168;
export const ROWS_NAME_PADDING = 24;
export const ROWS_CHROME = 48 + 14;

/** The widest viewport at which the rows table is vetoed beside `nav`. */
export function rowsVetoMaxWidth(nav: number): number {
  return nav + ROWS_CHROME + ROWS_FIXED_COLUMNS + ROWS_NAME_PADDING + ROWS_NAME_FLOOR - 1;
}

/**
 * The widest viewport at which the expanded sidebar still vetoes the rows
 * layout: 925px. Beside the rail it is 749px.
 *
 * The 640px breakpoint is a viewport width, but what decides whether five
 * facts fit on one line is the width of the content column beside the
 * navigation. SUB-149 first extended the veto by the 176px the sidebar takes
 * over the rail, to 816px, which stopped the table scrolling the page
 * sideways but still let rows through with an 86px name cell: at 820px beside
 * the sidebar 25 of the 26 demo names ended in an ellipsis, and beside the
 * rail at 641px the cell was the same 86px. A row that fits by cutting the
 * name to "Postgr…" has not fitted.
 *
 * So the veto now holds until the name cell reaches its floor, whichever
 * navigation is showing. Cards keep every fact the row shows, and wrap the
 * name instead of cutting it.
 */
export const SIDEBAR_VETO_MAX_WIDTH = rowsVetoMaxWidth(SIDEBAR_WIDTH);
export const RAIL_VETO_MAX_WIDTH = rowsVetoMaxWidth(RAIL_WIDTH);

/**
 * True when the content column beside the navigation is too narrow for the
 * rows table to give a name its floor, although the viewport is above the
 * breakpoint.
 *
 * Both queries are subscribed to on every render because hooks cannot be
 * skipped; the sidebar only decides which one counts. Below the breakpoint
 * the phone veto owns the answer.
 */
export function useRowsSqueeze(sidebarCollapsed: boolean): boolean {
  const narrow = useCompactViewport();
  const besideSidebar = useMediaQuery(`(max-width: ${SIDEBAR_VETO_MAX_WIDTH}px)`);
  const besideRail = useMediaQuery(`(max-width: ${RAIL_VETO_MAX_WIDTH}px)`);
  return !narrow && (sidebarCollapsed ? besideRail : besideSidebar);
}
