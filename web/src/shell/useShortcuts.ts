import { useEffect } from "react";

/**
 * Whether a keystroke should be left alone because the user is typing.
 *
 * The dashboard has a search field, and `Cmd/Ctrl + B` in a text box is "make
 * this bold" everywhere else on the web. Swallowing it there would be the kind
 * of shortcut that makes people distrust the page. `isContentEditable` covers
 * rich editors that are neither input nor textarea.
 */
function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return (
    tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable
  );
}

export type Shortcuts = {
  /** Cmd/Ctrl + B. */
  onToggleSidebar: () => void;
  /** Esc, and only while it has somewhere to go. */
  onEscape?: () => void;
};

/**
 * The shell's two keyboard shortcuts (DESIGN.md §7).
 *
 * Registered once on `window` rather than per component: they are global by
 * definition, and a listener per control would fire several times per press.
 * `keydown` and not `keyup`, because a shortcut should fire when it is pressed.
 * Auto-repeat is dropped explicitly (`event.repeat`): both actions are toggles,
 * so repeating one is a flicker that ends in an arbitrary state.
 *
 * Esc is passed as `undefined` when there is nothing to leave, so the handler
 * is not registered at all and Esc keeps its browser meaning (dismissing an
 * autocomplete, cancelling an IME composition) everywhere else.
 */
export function useShellShortcuts({ onToggleSidebar, onEscape }: Shortcuts): void {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        // Esc is allowed to fire while typing: leaving a full-screen view is
        // more urgent than whatever the focused field would have done, and
        // the wall has no text field anyway.
        if (onEscape !== undefined) {
          onEscape();
        }
        return;
      }
      const chord = (event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey;
      if (chord && (event.key === "b" || event.key === "B")) {
        if (isTyping(event.target)) return;
        // Claim the chord before the repeat check, so holding the keys never
        // falls through to the browser's own Cmd/Ctrl+B halfway through.
        event.preventDefault();
        // Holding the chord fires keydown at the OS repeat rate. Toggling per
        // event means the sidebar flickers and lands wherever the key release
        // happens to fall — a coin flip, not a command.
        if (event.repeat) return;

        onToggleSidebar();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onToggleSidebar, onEscape]);
}
