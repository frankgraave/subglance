import { useEffect, useRef, type RefObject } from "react";
import { COMPACT_MAX_WIDTH } from "../layout/useMediaQuery";
import { CloseIcon } from "./icons";
import { Sidebar } from "./Sidebar";

/**
 * The primary navigation on a phone: an overlay drawer, not a rail.
 *
 * On a laptop the sidebar collapses to a 56px icon rail, which keeps every
 * destination one click away while costing almost no width. At 375px that same
 * rail costs 15% of the screen permanently, and it spends it on icons for four
 * destinations that do not exist yet (DESIGN.md §12). The phone is the screen
 * you read *after* the alert arrived — the width belongs to the monitors, not
 * to navigation you are not currently using.
 *
 * So below the breakpoint the rail leaves the grid entirely and the same
 * `Sidebar` is rendered over the content on demand. Nothing about the
 * navigation changes, only where it lives; the topbar button that collapsed
 * the rail now opens the drawer.
 *
 * **It is modal, and modal has obligations.** Content covered by an overlay
 * that still answers the Tab key is a trap for anyone not using a mouse. The
 * background is made `inert` rather than focus-trapped by hand: it is the
 * platform's own mechanism, it also hides the content from assistive
 * technology, and it lets Tab reach the browser's chrome exactly as a mouse
 * user can. Focus moves into the drawer on open and returns to the button that
 * opened it on close, so the keyboard never loses its place.
 *
 * Rendered only while open. A drawer parked off-screen with `translateX` keeps
 * its links focusable and its text readable to a screen reader, which is the
 * most common way this pattern is got wrong; `null` cannot be focused.
 */
export type NavDrawerProps = {
  open: boolean;
  /** Called on the close button, the scrim, and when the viewport grows. */
  onClose: () => void;
  /**
   * Holds the element focus goes back to on close, captured by the caller
   * *before* it opened the drawer. A ref rather than a value so the caller
   * does not have to re-render to record a DOM node. See the effect below for
   * why this cannot be worked out from inside.
   */
  returnFocusRef?: RefObject<HTMLElement | null>;
  instance?: string;
};

export function NavDrawer({ open, onClose, returnFocusRef, instance }: NavDrawerProps) {
  const closerRef = useRef<HTMLButtonElement | null>(null);

  /*
   * Move focus in, and put it back where it came from.
   *
   * The close button rather than the first link: it is the one control that is
   * certain to exist, and starting on "close" means a screen reader announces
   * a way out before it announces five destinations.
   *
   * **The opener has to be handed to us; we cannot look it up.** React applies
   * the DOM mutations for a commit before it runs the effects for that commit,
   * and one of those mutations is marking the shell `inert`. The HTML
   * standard's focus fixup rule fires the moment a focused element becomes
   * inert and moves focus to the document body — so by the time this effect
   * reads `document.activeElement`, the topbar button that opened the drawer
   * is already gone and we would "restore" focus to `body`. The caller captures
   * it in its click handler, before the state update that causes any of this.
   *
   * The `document.activeElement` fallback still covers the case where the
   * drawer is opened by something that did not bother to capture an opener.
   */
  useEffect(() => {
    if (!open) return undefined;
    const fallback = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const returnTo = returnFocusRef?.current ?? fallback;
    closerRef.current?.focus();
    return () => {
      // Only restore if the element is still in the document; a drawer that
      // outlives its trigger must not throw on the way out.
      if (returnTo !== null && returnTo.isConnected) returnTo.focus();
    };
  }, [open, returnFocusRef]);

  /*
   * Close when the viewport grows past the breakpoint.
   *
   * Rotating a phone to landscape crosses 640px. Without this the scrim stays
   * over a layout that now has room for the rail, and the only way out is a
   * close button drawn over a page that no longer needs covering. `onClose` is
   * called from the listener, never during the effect itself.
   */
  useEffect(() => {
    if (!open) return undefined;
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return undefined;
    const query = window.matchMedia(`(max-width: ${COMPACT_MAX_WIDTH}px)`);
    const onChange = (event: MediaQueryListEvent) => {
      if (!event.matches) onClose();
    };
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, [open, onClose]);

  /*
   * Hold the page still underneath.
   *
   * Without this, scrolling the drawer past its last item scrolls the
   * dashboard behind it, so closing the drawer reveals a page that moved while
   * you were not looking at it.
   */
  useEffect(() => {
    if (!open) return undefined;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [open]);

  if (!open) return null;

  return (
    <>
      {/*
       * The scrim is a mouse affordance and nothing else: it duplicates the
       * close button, so it is hidden from assistive technology rather than
       * announced as a second, unlabelled way to do the same thing.
       */}
      <div className="shell-scrim" aria-hidden="true" onClick={onClose} />
      <div
        className="shell-drawer"
        role="dialog"
        aria-modal="true"
        aria-label="Navigation"
      >
        <button
          type="button"
          className="shell-icon-btn shell-drawer-close"
          ref={closerRef}
          onClick={onClose}
          aria-label="Close navigation"
        >
          <CloseIcon />
        </button>
        {/*
         * The same sidebar the laptop gets, always expanded: a drawer you
         * opened deliberately has no reason to show you icons only.
         */}
        <Sidebar collapsed={false} instance={instance} />
      </div>
    </>
  );
}
