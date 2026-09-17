import { useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { ReactNode } from "react";
import {
  getToolbarSlot,
  getTopbarSlot,
  getTopbarSlotServerSnapshot,
  subscribeToToolbarSlot,
  subscribeToTopbarSlot,
} from "./topbarSlot";

/**
 * Puts a screen's own controls into one of the shell's two bars.
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
function Slotted({
  children,
  subscribe,
  read,
}: {
  children: ReactNode;
  subscribe: (listener: () => void) => () => void;
  read: () => HTMLElement | null;
}) {
  const target = useSyncExternalStore(
    subscribe,
    read,
    getTopbarSlotServerSnapshot,
  );
  // No slot is not an error: the status wall renders no chrome at all, and a
  // screen mounted under it simply has nowhere to put its tools.
  if (target === null) return null;
  return createPortal(children, target);
}

/** Controls for the masthead — the bar that is identical on every screen. */
export function TopbarTools({ children }: { children: ReactNode }) {
  return (
    <Slotted subscribe={subscribeToTopbarSlot} read={getTopbarSlot}>
      {children}
    </Slotted>
  );
}

/**
 * Controls for the page toolbar — the bar that changes with the route.
 *
 * This is where sorting, grouping and filtering go. The masthead above it
 * holds only what is true everywhere, so that moving between screens never
 * moves the controls that are always there (SUB-138).
 */
export function ToolbarTools({ children }: { children: ReactNode }) {
  return (
    <Slotted subscribe={subscribeToToolbarSlot} read={getToolbarSlot}>
      {children}
    </Slotted>
  );
}
