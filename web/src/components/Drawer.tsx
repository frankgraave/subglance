import { useCallback, useEffect, useId, useRef } from "react";
import type { KeyboardEvent, ReactNode } from "react";

/**
 * The drawer (DESIGN.md §8).
 *
 * A full-height panel from the right with the page dimmed behind it, holding
 * the same card and panel structure used everywhere else. A drawer is a place
 * to put the product's existing surfaces, not a second visual language.
 *
 * Three behaviours are the substance of the component rather than polish:
 * Escape closes, focus is trapped inside the panel while it is open, and on
 * close focus returns to whatever opened it. Without the last one a keyboard
 * user is dropped at the top of the document and has lost their place — the
 * drawer covered the page, so there is nothing on screen to orient by.
 *
 * Movement uses `--dur-panel` and `--ease` (see `drawer.css`). Under
 * `prefers-reduced-motion` the slide is dropped but the drawer still appears:
 * suppressing the animation is the accommodation, suppressing the panel would
 * withhold what the user asked for.
 */

const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export type DrawerProps = {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  /** Actions closing the panel, rendered on their own rule at the bottom. */
  footer?: ReactNode;
  /** The word on the close control, for assistive technology. */
  closeLabel?: string;
  className?: string;
};

export function Drawer({
  open,
  onClose,
  title,
  children,
  footer,
  closeLabel = "Close",
  className,
}: DrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  const titleId = useId();

  useEffect(() => {
    if (!open) return;
    // Captured at open, before focus moves into the panel, because by the time
    // the drawer closes the element that opened it is no longer focused.
    openerRef.current = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    const first = panel?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel)?.focus();
    return () => {
      // Returning focus is the whole point; guarding on `isConnected` keeps it
      // from throwing when the opener was unmounted along with the drawer.
      const opener = openerRef.current;
      if (opener?.isConnected) opener.focus();
    };
  }, [open]);

  const onKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      // The trap. Tab out of either end and it wraps, so the page behind the
      // scrim — which cannot be clicked — cannot be tabbed into either.
      const nodes = [
        ...(panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []),
      ];
      if (nodes.length === 0) return;
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    },
    [onClose],
  );

  if (!open) return null;

  return (
    <>
      {/* The scrim dims the page and is also the click target for dismissal,
          which is the pointer equivalent of Escape. */}
      <button
        type="button"
        className="drawer-scrim"
        onClick={onClose}
        aria-hidden="true"
        tabIndex={-1}
      />
      {/* A dialog is where Escape and the focus trap have to be listened for:
          the keys belong to the panel as a whole, not to any one control in
          it, so the handler cannot move to a child without losing the keys
          pressed between them. */}
      {/* oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions */}
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={className ? `drawer-panel ${className}` : "drawer-panel"}
        onKeyDown={onKeyDown}
      >
        <div className="drawer-head">
          <h2 id={titleId} className="drawer-title">
            {title}
          </h2>
          <button
            type="button"
            className="drawer-close"
            aria-label={closeLabel}
            onClick={onClose}
          >
            <span aria-hidden="true">&times;</span>
          </button>
        </div>
        <div className="drawer-body">{children}</div>
        {footer ? <div className="drawer-foot">{footer}</div> : null}
      </div>
    </>
  );
}
