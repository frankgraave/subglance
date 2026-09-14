import type { ReactNode } from "react";

/**
 * One measured number, drawn as a reading rather than as text (DESIGN.md §10).
 *
 * Three decisions live here, and all three failed silently before.
 *
 * **A zero is dimmer than a real measurement, and brighter than nothing.**
 * This is the one that sounds simpler than it is. `0` is *data*: the check ran
 * and the answer was zero. It is not a missing value. So a column of zeros
 * should step back rather than compete with the readings beside it — but it
 * must still be plainly a number someone measured, which is why it takes
 * `--ink-zero` (one step under a live reading) and not the tone that means
 * "no data". If a zero and an em dash rendered alike, the dashboard would be
 * claiming it never checked when it did, which is worse than either state on
 * its own. `data-zero` and `data-empty` are therefore two attributes, never one.
 *
 * **Numbers are mono and right-aligned.** Right alignment is what makes a
 * column comparable at a glance: units line up under units and the eye reads
 * magnitude from the left edge of the digits. The mono face already carries
 * `--numeric-mono` (slashed zero, tabular figures), so the columns line up
 * without any call site remembering to ask.
 *
 * **A warning survives without colour.** A value carrying a caveat gets a
 * dotted underline *and* an amber glyph, not an amber tint. Anyone who is
 * colour-blind, or reading a screen in black and white, still sees both marks;
 * the tint is the third copy of the signal, not the signal.
 */

export type ValueProps = {
  /**
   * The formatted reading, already carrying its unit — "87 ms", "100%".
   * Null means there is no measurement at all.
   */
  children?: ReactNode;
  /**
   * The raw number behind the text, when there is one.
   *
   * Passed separately because "is this a zero" is a fact about the
   * measurement, not about its formatting: "0 ms", "0%" and "0.00 s" are all
   * the same reading and must all dim, and sniffing the string for a `0`
   * would also dim "10 ms".
   */
  value?: number | null;
  /**
   * A caveat about this reading — "measured from one probe", "stale by 6m".
   * Its presence draws the dotted underline and the glyph; its text is what a
   * screen reader hears and what the tooltip shows.
   */
  warning?: string;
  className?: string;
};

export function Value({ children, value, warning, className }: ValueProps) {
  // An absent measurement, which is a different statement from a zero: no
  // number behind the text, and nothing rendered in its place either.
  const empty =
    (value === null || value === undefined) &&
    (children === null || children === undefined);
  const zero = value === 0;

  return (
    <span
      className={join("value", className)}
      data-zero={zero ? "true" : undefined}
      data-empty={empty ? "true" : undefined}
      data-warn={warning === undefined ? undefined : "true"}
      {...(warning === undefined ? {} : { title: warning })}
    >
      <span className="value-text">{children}</span>
      {warning === undefined ? null : (
        // A real glyph with a real accessible name, not a decorative dot: the
        // caveat is information, and a reader that skips it gets a different
        // page from the one on screen.
        <span className="value-warn" role="img" aria-label={warning}>
          {"\u26A0"}
        </span>
      )}
    </span>
  );
}

function join(base: string, extra?: string): string {
  return extra ? `${base} ${extra}` : base;
}
