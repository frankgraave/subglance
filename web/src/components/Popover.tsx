import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";

/**
 * A panel that hangs from the button that opens it (SUB-183).
 *
 * The dashboard's Filter and View controls each open one. It is not `Menu`:
 * a menu holds verbs and closes when one is chosen, while this holds settings
 * that are changed several at a time — a radio per tag value, a layout, a
 * grouping — and stays open until the reader is done. So it is a non-modal
 * `dialog` named by its label, and focus moves into it on open.
 *
 * It closes on Escape, on a press outside it and when focus leaves it, and
 * Escape hands focus back to the button: a keyboard user who opened it is put
 * back where they were rather than at the top of the document. It wears the
 * menu's floating panel (`menu.css`), so the two read as one kind of surface.
 */

const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export type PopoverProps = {
  /** The dialog's accessible name. */
  label: string;
  /** The button's face: a glyph, a word, a count. */
  trigger: ReactNode;
  /**
   * The button's accessible name. Required, because its face is a glyph plus
   * a word, and what a reader makes of an unnamed inline `<svg>` varies
   * (AGENTS.md).
   */
  triggerLabel: string;
  triggerClassName?: string;
  /** A class for the panel, beside the menu's own. */
  className?: string;
  /** The edge the panel lines up with. End, for a button at a row's right. */
  align?: "start" | "end";
  /** The panel's content; `close` shuts it and returns focus to the button. */
  children: (close: () => void) => ReactNode;
};

export function Popover({
  label,
  trigger,
  triggerLabel,
  triggerClassName,
  className,
  align = "end",
  children,
}: PopoverProps) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const panelId = useId();
  const triggerId = useId();

  // The button is found by its id rather than held in a ref: `close` is
  // handed to the panel's content during render, and a ref read reachable
  // from render is what the linter refuses.
  const close = useCallback((returnFocus: boolean) => {
    setOpen(false);
    if (returnFocus) document.getElementById(triggerId)?.focus();
  }, [triggerId]);
  // What the content calls to finish: shut, and hand focus back.
  const done = useCallback(() => close(true), [close]);

  useEffect(() => {
    if (!open) return;
    const panel = panelRef.current;
    (panel?.querySelector<HTMLElement>(FOCUSABLE) ?? panel)?.focus();
    // A press or a focus move anywhere outside the button and the panel closes
    // it, without taking focus back: the reader has already gone elsewhere.
    const outside = (event: Event) => {
      if (!rootRef.current?.contains(event.target as Node)) close(false);
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("focusin", outside);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("focusin", outside);
    };
  }, [open, close]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    // Only this panel closes: a drawer or the page behind it keeps its own.
    event.stopPropagation();
    close(true);
  };

  return (
    <div ref={rootRef} className="menu">
      <button
        id={triggerId}
        type="button"
        className={triggerClassName}
        aria-label={triggerLabel}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((was) => !was)}
      >
        {trigger}
      </button>
      {open ? (
        // The keys belong to the panel as a whole, as in `Drawer`.
        // oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions
        <div
          ref={panelRef}
          id={panelId}
          role="dialog"
          aria-label={label}
          tabIndex={-1}
          className={className ? `menu-panel ${className}` : "menu-panel"}
          data-align={align}
          onKeyDown={onKeyDown}
        >
          {children(done)}
        </div>
      ) : null}
    </div>
  );
}
