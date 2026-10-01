import { useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { ReactNode } from "react";
import {
  getToolbarSlot,
  getToolbarSlotServerSnapshot,
  subscribeToToolbarSlot,
} from "./toolbarSlot";

/**
 * Puts a screen's own controls into the shell's page toolbar.
 *
 * The toolbar is rendered by `App`, above the route, and a filter that belongs
 * to the monitors page has its state inside the monitors page. Passing the
 * node up as a prop would mean hoisting every piece of screen state into the
 * shell, where the shell would then have to know what a type facet is.
 *
 * A portal keeps the state where it belongs and puts the pixels where the user
 * expects them: the page renders its controls in its own tree, React moves the
 * DOM nodes into the bar. Events still bubble through the React tree, so a
 * handler written next to the list it filters keeps working unchanged — and a
 * screen that unmounts takes its controls out of the bar with it, which is the
 * behaviour a route-bound toolbar needs and a prop threaded through the shell
 * would have to reimplement.
 *
 * Sorting, grouping, filtering and a screen's own view controls go here. There
 * is no counterpart for the masthead (SUB-182): the masthead holds only what
 * works on every screen, and renders that itself.
 */
export function ToolbarTools({ children }: { children: ReactNode }) {
  const target = useSyncExternalStore(
    subscribeToToolbarSlot,
    getToolbarSlot,
    getToolbarSlotServerSnapshot,
  );
  // No slot is not an error: the status wall renders no chrome at all, and a
  // screen mounted under it simply has nowhere to put its tools.
  if (target === null) return null;
  return createPortal(children, target);
}
