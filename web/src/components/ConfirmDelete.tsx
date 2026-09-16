import { useId, useState } from "react";
import { Drawer } from "./Drawer";

/**
 * Deleting asks you to retype the name (DESIGN.md §7.5).
 *
 * "Are you sure?" is a reflex — the hand is already moving toward the button
 * that makes the dialog go away, and the second click costs no more thought
 * than the first. Retyping is a decision: it cannot be completed without
 * reading what is about to be destroyed, because the name has to be copied
 * from the sentence that names it.
 *
 * Shared rather than written per screen, and that is the point of this file.
 * Monitors and notification channels both delete something unrecoverable, and
 * both had a one-click confirmation — the rule existed in the design document
 * and nowhere in the code, so each new destructive action rediscovered it or
 * did not. A screen can now only opt out of this by not using the component,
 * which is a visible choice rather than an oversight.
 *
 * The consequence is a required prop, not an optional flourish. What
 * disappears alongside the thing itself — heartbeats, uptime history, an
 * incident record, stored credentials, the monitors that were routed here —
 * is exactly what the reader cannot infer from a button that says Delete.
 */
export type ConfirmDeleteProps = {
  open: boolean;
  onClose: () => void;
  /** What is being deleted, e.g. "monitor" or "notification channel". */
  kind: string;
  /** The exact name the reader must retype. */
  name: string;
  /**
   * What is destroyed besides the thing itself. Stated in full, because this
   * is the sentence the retyping exists to make someone read.
   */
  consequence: string;
  onConfirm: () => void;
};

export function ConfirmDelete({
  open,
  onClose,
  kind,
  name,
  consequence,
  onConfirm,
}: ConfirmDeleteProps) {
  const [typed, setTyped] = useState("");
  const inputId = useId();

  /*
   * Exact match, not trimmed and not case-folded.
   *
   * A forgiving comparison would accept a name the reader never actually
   * read — and leniency here buys nothing, since the name is on screen
   * directly above the field. Names differing only in case or trailing space
   * are also a real way to delete the wrong one of two similar channels.
   */
  const matches = typed === name;

  /*
   * The field is cleared whenever the dialog opens or closes, so a half-typed
   * name from a cancelled deletion cannot be sitting there — already matching
   * — the next time this opens for something else.
   */
  const close = () => {
    setTyped("");
    onClose();
  };

  return (
    <Drawer
      open={open}
      onClose={close}
      title={`Delete ${kind}`}
      footer={
        <>
          <button type="button" className="add-button" onClick={close}>
            Keep it
          </button>
          <button
            type="button"
            className="add-button inv-act--danger"
            disabled={!matches}
            /*
             * The name is in the accessible name as well as the visible label:
             * "Delete auth" tells a screen-reader user which thing this button
             * destroys, where a bare "Delete" in a list of similar dialogs
             * does not. The visible text is a prefix of it (WCAG 2.5.3).
             */
            onClick={() => {
              setTyped("");
              onConfirm();
            }}
          >
            Delete {name}
          </button>
        </>
      }
    >
      <p className="add-help">{consequence}</p>
      <div className="add-field">
        <label className="add-label" htmlFor={inputId}>
          Type <span className="nt-mask">{name}</span> to confirm
        </label>
        <input
          id={inputId}
          className="add-input"
          type="text"
          value={typed}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setTyped(event.target.value)}
        />
        <p className="add-help">
          {matches
            ? "Names match. Deleting cannot be undone."
            : "The delete button stays disabled until the name matches exactly."}
        </p>
      </div>
    </Drawer>
  );
}
