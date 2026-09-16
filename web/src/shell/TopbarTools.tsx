import { useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { ReactNode } from "react";
import {
  getTopbarSlot,
  getTopbarSlotServerSnapshot,
  subscribeToTopbarSlot,
} from "./topbarSlot";

/**
 * Puts a screen's own controls into the shell's toolbar (SUB-131).
 *
 * `Topbar` has always had a `children` slot documented for "controls owned by
 * the page", and nothing ever passed anything into it — so every screen showed
 * the dashboard's layout switcher, including the two screens that have no
 * layouts, while each screen's real tools sat in a second bar underneath. The
 * slot was unusable as a prop for a structural reason: the topbar is rendered
 * by `App`, above the route, and a filter that belongs to the monitors page
 * has its state inside the monitors page. Passing the node up would mean
 * hoisting three pieces of screen state into the shell, where the shell would
 * then have to know what a type facet is.
 *
 * A portal keeps the state where it belongs and puts the pixels where the user
 * expects them: the page renders its controls in its own tree, React moves the
 * DOM nodes into the bar. Events still bubble through the React tree, so a
 * handler written next to the list it filters keeps working unchanged — and a
 * screen that unmounts takes its controls out of the bar with it, which is the
 * behaviour a route-bound toolbar needs and a prop threaded through the shell
 * would have to reimplement.
 */
export function TopbarTools({ children }: { children: ReactNode }) {
  const target = useSyncExternalStore(
    subscribeToTopbarSlot,
    getTopbarSlot,
    getTopbarSlotServerSnapshot,
  );
  // No slot is not an error: the status wall renders no topbar at all, and a
  // screen mounted under it simply has nowhere to put its tools.
  if (target === null) return null;
  return createPortal(children, target);
}
