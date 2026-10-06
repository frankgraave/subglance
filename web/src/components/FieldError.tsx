import type { ReactNode } from "react";
import { IconAlert } from "./icons";

/**
 * A refusal: the sentence under a field (or under a form) that says what is
 * wrong with what was entered, and what would be right (DESIGN.md §7.2).
 *
 * The words are set in the body ink and the alert glyph carries the error
 * colour. The red the product uses for a failure measures 4.49:1 on a light
 * settings panel and 4.28:1 on a dark one at the helper size, under the 4.5:1
 * a sentence needs, so a red sentence was a sentence some readers could not
 * read. A glyph is a graphic and needs 3:1, which the same red clears on
 * every surface. Colour is still not the only signal: the field itself wears
 * `aria-invalid` and a red border, and the message is announced.
 *
 * One component draws every refusal, so the glyph cannot be left off one of
 * them and the ink cannot be overridden back to red in a feature stylesheet;
 * `FieldError.test.tsx` refuses the class written anywhere else.
 *
 * `announce` is off only for a message about something the reader did not do
 * (a list that failed to load beside a form that still works), which should
 * not interrupt them.
 */
export function FieldError({ id, children, announce = true }: {
  id?: string;
  children: ReactNode;
  announce?: boolean;
}) {
  return <p className="field-error" id={id} role={announce ? "alert" : undefined}>
    <IconAlert /><span>{children}</span>
  </p>;
}
