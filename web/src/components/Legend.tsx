import type { ReactNode } from "react";

/**
 * The legend (DESIGN.md §2.5, §8).
 *
 * A coloured mark, what it means, and optionally the number attached to it.
 *
 * The decision worth defending is the tone of the label. A legend is quiet
 * because it is small, monospaced and uppercase — not because it is faded. The
 * codebase has reached for `--ink-3` and `--ink-4` here before, which drops
 * below the contrast floor on the one line that tells a reader what a colour
 * means. The shared `caps-legend` role takes `--ink-2` instead; see
 * `legend.css` for the measured numbers. Anything darker than that is a
 * regression, not a refinement.
 *
 * The list is a `<dl>` because that is what it is: terms and their values. A
 * legend with no values degrades to plain items, so the markup follows.
 */

/** The statuses a marker can name. Anything not in this set draws neutral,
 *  because an unknown colour is worse than no colour. */
export type LegendStatus = "up" | "warn" | "down" | "idle";

export type LegendItem = {
  key: string;
  label: ReactNode;
  /** The colour of the mark. Omitted means a neutral dot. */
  marker?: LegendStatus;
  /** Right-aligned, mono, tabular. Omitted means the row is label-only. */
  value?: ReactNode;
};

export type LegendProps = {
  items: LegendItem[];
  /** Names the legend for assistive technology when the surrounding heading
   *  does not already do it. */
  label?: string;
  className?: string;
};

export function Legend({ items, label, className }: LegendProps) {
  return (
    <dl
      className={className ? `legend ${className}` : "legend"}
      {...(label ? { "aria-label": label } : {})}
    >
      {items.map((item) => (
        <div
          key={item.key}
          className={
            item.value === undefined ? "legend-item legend-item--flow" : "legend-item"
          }
        >
          {/* The mark repeats the colour of the series it names, so it is
              decorative: the label beside it already carries the meaning for
              anyone who cannot see the colour. */}
          <span
            className="legend-marker"
            data-status={item.marker}
            aria-hidden="true"
          />
          <dt className="legend-label">{item.label}</dt>
          {item.value !== undefined ? (
            <dd className="legend-value">{item.value}</dd>
          ) : null}
        </div>
      ))}
    </dl>
  );
}
