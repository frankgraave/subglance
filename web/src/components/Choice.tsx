import { useLayoutEffect, useRef, type InputHTMLAttributes, type ReactElement, type ReactNode } from "react";

/**
 * The product's checkbox and radio (DESIGN.md §8.9).
 *
 * Real `<input>` elements, restyled rather than replaced. The native element
 * keeps everything a screen reader, a keyboard and a form rely on — the role,
 * the checked and mixed states, Space to toggle, arrow keys between radios,
 * a click on the label, the value in `FormData` — so none of that is
 * reimplemented here. Only the drawing changes: `choice.css` takes the
 * platform's rendering away and paints the box from tokens, so it is the same
 * mark in every browser and in both themes.
 *
 * Before this there were eight of these in six files. Six were native boxes
 * tinted with `accent-color` and stretched to the 26px compact square by
 * three different stylesheets; two were not styled at all and drew at the
 * browser's 13px. `tokens.test.ts` now refuses a checkbox or radio written
 * anywhere else under `web/src`, and refuses `accent-color`, so a ninth
 * cannot be hand-rolled beside these.
 *
 * Pass `children` and the words become the label: the input is wrapped in a
 * `<label>`, so a click anywhere on the sentence toggles it, laid out the same
 * way on every screen. Leave them off for a box that is named some other way —
 * a row's selection box, whose name is the row's link, takes an `aria-label`.
 */
type ChoiceProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type"> & {
  /** The label, drawn beside the box. */
  children?: ReactNode;
};

export type CheckboxProps = ChoiceProps & {
  /**
   * Neither checked nor unchecked: some of a group, not all of it. A screen
   * reader announces it as "mixed".
   *
   * There is no HTML attribute for this, only a DOM property, so it is set on
   * the element after render. A click clears it and checks the box, which is
   * the platform's behaviour and the right one: the next state of "some" is a
   * decision, and the caller's `onChange` makes it.
   */
  indeterminate?: boolean;
};

export function Checkbox({
  indeterminate = false,
  children,
  className,
  ...rest
}: CheckboxProps) {
  const ref = useRef<HTMLInputElement>(null);
  // A layout effect, so the property is on the element before the browser
  // paints; a plain effect draws one frame of "unchecked" first.
  useLayoutEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate;
  }, [indeterminate]);
  return labelled(
    <input {...rest} ref={ref} type="checkbox" className={classes(className)} />,
    children,
  );
}

export function Radio({ children, className, ...rest }: ChoiceProps) {
  return labelled(
    <input {...rest} type="radio" className={classes(className)} />,
    children,
  );
}

function classes(extra?: string): string {
  return extra ? `choice ${extra}` : "choice";
}

function labelled(input: ReactElement, children: ReactNode) {
  if (children === undefined) return input;
  return (
    <label className="choice-label">
      {input}
      {children}
    </label>
  );
}
