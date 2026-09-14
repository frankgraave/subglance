import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";

/**
 * The menu (DESIGN.md §8).
 *
 * Every item is a title plus a description, with a glyph on the left. A bare
 * list of verbs — "Pause", "Reset", "Remove" — makes the reader guess what
 * each one touches, and guessing is expensive on a screen where the actions
 * change what is monitored. The description carries the actual content of the
 * item, so the type treats it as required rather than optional.
 *
 * Keyboard behaviour is the other half. Arrow keys move through the items,
 * Home and End jump to the ends, Escape closes and returns focus to the
 * trigger, and focus is drawn with the accent ring (see `menu.css`) rather
 * than a neutral outline, so there is one focus language in the product.
 */

export type MenuItem = {
  key: string;
  /** The verb. Short. */
  title: string;
  /** What the verb does. Required on purpose: see the note above. */
  description: string;
  icon?: ReactNode;
  /** `danger` colours the verb, not the sentence. */
  tone?: "neutral" | "danger";
  disabled?: boolean;
  onSelect?: () => void;
};

export type MenuProps = {
  /** Rendered as the button that opens the panel. */
  trigger: ReactNode;
  items: MenuItem[];
  /** Which edge the panel hangs from when it would otherwise run off-screen. */
  align?: "start" | "end";
  label?: string;
  className?: string;
};

export function Menu({
  trigger,
  items,
  align = "start",
  label = "Actions",
  className,
}: MenuProps) {
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const panelId = useId();

  // A disabled item is still readable but never lands under the cursor keys:
  // arrowing onto something that cannot be chosen is a dead end.
  const selectable = items
    .map((item, index) => (item.disabled ? -1 : index))
    .filter((index) => index >= 0);

  const close = useCallback((returnFocus: boolean) => {
    setOpen(false);
    // Closing without handing focus back would drop a keyboard user at the top
    // of the document, which is nowhere near where they were.
    if (returnFocus) triggerRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!triggerRef.current?.parentElement?.contains(target)) close(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [open, close]);

  useEffect(() => {
    if (!open) return;
    // The first selectable item takes focus on open, so the keyboard path and
    // the pointer path start in the same place.
    const first = selectable[0] ?? 0;
    setActiveIndex(first);
    itemRefs.current[first]?.focus();
    // `items` is deliberately not a dependency: re-focusing on every re-render
    // would fight the user's own arrow keys.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const move = (delta: number) => {
    if (selectable.length === 0) return;
    const current = selectable.indexOf(activeIndex);
    const next =
      selectable[(current + delta + selectable.length) % selectable.length];
    setActiveIndex(next);
    itemRefs.current[next]?.focus();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        move(1);
        break;
      case "ArrowUp":
        event.preventDefault();
        move(-1);
        break;
      case "Home":
        event.preventDefault();
        setActiveIndex(selectable[0] ?? 0);
        itemRefs.current[selectable[0] ?? 0]?.focus();
        break;
      case "End": {
        event.preventDefault();
        const last = selectable[selectable.length - 1] ?? 0;
        setActiveIndex(last);
        itemRefs.current[last]?.focus();
        break;
      }
      case "Escape":
        event.preventDefault();
        close(true);
        break;
      default:
        break;
    }
  };

  return (
    <div className={className ? `menu ${className}` : "menu"}>
      <button
        type="button"
        ref={triggerRef}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((was) => !was)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" && !open) {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        {trigger}
      </button>
      {open ? (
        <div
          id={panelId}
          role="menu"
          aria-label={label}
          /* The panel itself is never the focus target — the items are — but an
             interactive role has to be focusable for the platform to treat it
             as one, so it takes -1 rather than a place in the tab order. */
          tabIndex={-1}
          className="menu-panel"
          data-align={align}
          onKeyDown={onKeyDown}
        >
          {items.map((item, index) => (
            <button
              key={item.key}
              type="button"
              role="menuitem"
              className="menu-item"
              data-tone={item.tone ?? "neutral"}
              data-active={index === activeIndex}
              disabled={item.disabled}
              tabIndex={index === activeIndex ? 0 : -1}
              ref={(node) => {
                itemRefs.current[index] = node;
              }}
              onClick={() => {
                item.onSelect?.();
                close(true);
              }}
            >
              {/* Decorative: the title beside it already names the action. */}
              <span className="menu-item-icon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="menu-item-text">
                <span className="menu-item-title">{item.title}</span>
                <span className="menu-item-description">{item.description}</span>
              </span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
