import type { ButtonHTMLAttributes } from "react";
import { IconTrash } from "./icons";

/**
 * A worded destructive action: the Delete that commits, not one of a list's
 * bins (DESIGN.md §7.1).
 *
 * The words are in the body ink and the bin glyph carries the failure red, the
 * split `FieldError` makes for a refusal. A red label measured 3.9:1 on the
 * button's surface in the dark theme, under the 4.5:1 a word needs, and the
 * reset card's button and the delete drawer's were both drawn that way. A glyph
 * is a graphic and needs 3:1, which the same red clears in both themes. The
 * colour is not the only signal: the bin's shape says destructive in
 * greyscale, the word says Delete, and the button stays disabled until the
 * reader has retyped a name or a phrase.
 *
 * One component draws every one, so the glyph cannot be left off and the ink
 * cannot drift back to red; `DangerButton.test.tsx` refuses the class written
 * on a `<button>` anywhere else.
 *
 * `children` is the visible label and also the accessible name. A button that
 * is a glyph plus a word is named explicitly, because what a screen reader
 * makes of an unnamed inline `<svg>` depends on the reader.
 */
export function DangerButton({ children, type = "button", ...rest }: Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  "children" | "className" | "aria-label"
> & {
  children: string;
}) {
  return (
    <button {...rest} type={type} className="button button--danger" aria-label={children}>
      {children}
      <IconTrash />
    </button>
  );
}
