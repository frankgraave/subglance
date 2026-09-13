import { monitorPath } from "../shell/route";

/**
 * The monitor's name, as the way into its detail view.
 *
 * A real `<a href>`, not a `<div onClick>` and not a `<button>`. All three can
 * be made to open the page; only the anchor is a *link*, and the difference is
 * everything a link gets for free and a handler has to reimplement badly:
 * middle-click and Cmd-click open a second monitor in a new tab, right-click
 * offers "copy link address" so a URL can be pasted into a chat, hovering
 * shows the destination in the status bar, and a screen reader announces it as
 * a link to somewhere rather than a control that does something.
 *
 * The click handler is an optimisation on top of that, not the mechanism: it
 * swaps a full page reload for a client-side route change. When the user asks
 * for anything other than a plain left click — a modifier, a middle button —
 * it stands aside and lets the browser do what the user asked, which is the
 * step hand-rolled SPA links usually skip.
 *
 * The whole row is not clickable. A row carries selectable text (the target,
 * which people copy) and, in the card layout, a heartbeat bar with its own
 * hover tooltips; making the container a link makes selecting that text start
 * a navigation instead, and gives a screen reader one enormous link whose
 * accessible name is every fact on the line.
 */

export type MonitorLinkProps = {
  id: string;
  name: string;
  /** Called instead of a page load when the click is a plain left click. */
  onOpen?: (id: string) => void;
  className?: string;
};

export function MonitorLink({ id, name, onOpen, className }: MonitorLinkProps) {
  return (
    <a
      className={className}
      href={monitorPath(id)}
      onClick={(event) => {
        if (onOpen === undefined) return;
        // Modified clicks and anything that is not the primary button belong
        // to the browser: the user explicitly asked for a new tab, a new
        // window, or a download.
        if (
          event.defaultPrevented ||
          event.button !== 0 ||
          event.metaKey ||
          event.ctrlKey ||
          event.shiftKey ||
          event.altKey
        ) {
          return;
        }
        event.preventDefault();
        onOpen(id);
      }}
    >
      {name}
    </a>
  );
}
