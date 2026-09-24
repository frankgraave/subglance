import { flushSync } from "react-dom";
import { monitorPath } from "./route";

/**
 * The name a monitor's title carries across a dashboard <-> detail change.
 *
 * A view transition pairs one old element with one new element per name: the
 * link that was clicked and the page heading it opens. Both are tagged here,
 * and only for the length of one transition, never in a stylesheet. A list
 * holds one link per monitor, and two elements sharing a name on one screen
 * make the browser skip the whole transition.
 */
export const TITLE_MORPH = "monitor-title";

type Transition = { finished: Promise<unknown> };
type StartTransition = (update: () => void) => Transition;

export type Morph = {
  /** The element on the current screen the title morphs out of. */
  from?: () => Element | null | undefined;
  /** The element on the next screen it morphs into, read after the update. */
  to?: () => Element | null | undefined;
};

/**
 * Motion is opt-in (DESIGN.md §9): without a media query to ask, or with
 * reduced motion requested, the answer is no.
 */
function motionAllowed(view: Window): boolean {
  if (typeof view.matchMedia !== "function") return false;
  return !view.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * A title that starts or ends outside the viewport would fly in from
 * somewhere the reader was not looking. Those screens get the plain
 * cross-fade instead.
 */
function onScreen(element: Element, view: Window): boolean {
  const box = element.getBoundingClientRect();
  if (box.width === 0 || box.height === 0) return false;
  return (
    box.bottom > 0 &&
    box.right > 0 &&
    box.top < view.innerHeight &&
    box.left < view.innerWidth
  );
}

/**
 * Runs a route change inside a view transition, so the screen morphs instead
 * of being swapped.
 *
 * The update is the whole change and always runs. Without the View
 * Transitions API, or with reduced motion requested, it runs synchronously
 * and nothing else happens: the movement is decoration on a navigation that
 * is already correct without it.
 *
 * `flushSync` is not optional. The browser captures the new state as soon as
 * the callback returns; a React update that is only scheduled by then would
 * be captured as the old screen, and the transition would animate from the
 * dashboard to the dashboard.
 */
export function morphNavigation(
  update: () => void,
  morph: Morph = {},
  doc: Document = document,
): void {
  const view = doc.defaultView;
  const start = (doc as unknown as { startViewTransition?: StartTransition })
    .startViewTransition;
  if (typeof start !== "function" || view === null || !motionAllowed(view)) {
    update();
    return;
  }
  const tagged: HTMLElement[] = [];
  const tag = (element: Element | null | undefined) => {
    if (!(element instanceof view.HTMLElement) || !onScreen(element, view)) return;
    element.style.setProperty("view-transition-name", TITLE_MORPH);
    tagged.push(element);
  };
  tag(morph.from?.());
  const transition = start.call(doc, () => {
    // The old screen is already captured; its tag must not survive into the
    // new one, where the same element can still be mounted (a link in a list
    // that stays on screen) and would be a second holder of the name.
    for (const element of tagged.splice(0)) {
      element.style.removeProperty("view-transition-name");
    }
    flushSync(update);
    tag(morph.to?.());
  });
  const untag = () => {
    for (const element of tagged.splice(0)) {
      element.style.removeProperty("view-transition-name");
    }
  };
  // `finished` settles when the transition is skipped too; either way the
  // name has to come off, or the next transition finds two titles.
  transition.finished.then(untag, untag);
}

/**
 * The link on the current screen that names this monitor, if one is showing.
 *
 * Found by its address rather than a ref: every list layout renders its own
 * `MonitorLink`, and the address is the one thing they all agree on. The first
 * one inside the viewport wins, so a screen that names the monitor twice
 * morphs from the copy the reader can see.
 */
export function monitorTitleLink(root: Element | null, id: string): Element | null {
  if (root === null) return null;
  const view = root.ownerDocument.defaultView;
  const href = monitorPath(id);
  for (const link of root.querySelectorAll("a[href]")) {
    if (link.getAttribute("href") !== href) continue;
    if (view === null || onScreen(link, view)) return link;
  }
  return null;
}

/** The detail page's heading: the monitor's name, as the page title. */
export function detailTitle(root: Element | null): Element | null {
  return root?.querySelector(".mon-detail-name") ?? null;
}
