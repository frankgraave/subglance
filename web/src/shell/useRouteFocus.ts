import { useEffect, useRef } from "react";

/**
 * Moves focus to the new screen when the route changes.
 *
 * A client-side navigation replaces the whole page for a sighted user and
 * nothing at all for a keyboard or screen-reader user: focus stays on the link
 * that was activated, which now points at something that is no longer on
 * screen, and the next Tab continues from a position in a document that has
 * been replaced. The browser does this for a real page load; a single-page app
 * has to do it by hand.
 *
 * Focus goes to the region, not to its heading. A heading that takes focus has
 * to be given `tabIndex={-1}` at every call site and reads out as a focusable
 * thing that does nothing; focusing the container makes a screen reader
 * announce the region and then its first heading, which is what a page load
 * sounds like.
 *
 * **Not on first render.** On arrival focus belongs where the browser put it —
 * stealing it there is the behaviour `jsx-a11y/no-autofocus` exists to
 * complain about, and it would throw away a restored scroll position. The ref
 * below is what makes the first route a no-op.
 */
export function useRouteFocus(
  key: string,
  ref: React.RefObject<HTMLElement | null>,
): void {
  const previous = useRef<string | null>(null);
  useEffect(() => {
    if (previous.current === null || previous.current === key) {
      previous.current = key;
      return;
    }
    previous.current = key;
    const node = ref.current;
    if (!node) return;
    // `preventScroll`: the caller already decided where the new page starts,
    // and focusing a container the size of the viewport would otherwise scroll
    // it back to wherever the browser thinks the element's top edge is.
    node.focus({ preventScroll: true });
  }, [key, ref]);
}
