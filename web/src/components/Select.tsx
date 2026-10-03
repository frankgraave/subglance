import type { ComponentProps } from "react";

/**
 * The product's dropdown (DESIGN.md §8.11).
 *
 * A real `<select>`, restyled rather than replaced. The OS picker on a phone,
 * type-to-find, the listbox role, the `<label>` association and the value in
 * `FormData` all stay the browser's, and every list in the product is a
 * handful of values long, which is the case where native is simply better.
 * Only the closed face changes: `select.css` takes the platform's arrow away
 * and paints one from tokens, so the arrow is the same in every browser and
 * both themes instead of whatever the platform draws beside a field that is
 * otherwise drawn from tokens.
 *
 * The caller's class says which field this is (a form field's `input`, the toolbar's own class)
 * and stays first; this adds `select`, which draws the arrow. `tokens.test.ts` refuses a `<select>`
 * written anywhere else under `web/src`, so a new one cannot come back with
 * the platform's arrow.
 */
export function Select({ className, ...rest }: ComponentProps<"select">) {
  return <select {...rest} className={className ? `${className} select` : "select"} />;
}
