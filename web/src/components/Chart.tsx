import type { ReactNode } from "react";

/**
 * The chart chrome (DESIGN.md §13).
 *
 * This component draws everything around a plot and none of the plot itself:
 * the caller keeps drawing its own marks and hands them in as children. The
 * reason to split it that way is that the chrome is where the reference
 * style's decisions live, and repeating those decisions once per chart is how
 * they drift.
 *
 * Four of them, and why each one earns its place:
 *
 * - **No axis furniture.** Faint horizontal gridlines and nothing else: no
 *   axis labels, no ticks, no frame. Axes and frames are ink that is not data,
 *   and on a dashboard somebody stares at all day every line costs attention
 *   it never pays back. The gridlines stay because they are what makes two
 *   heights comparable without a scale beside them.
 * - **The time range sits in the two bottom corners instead of an x-axis.**
 *   A row of tick labels spends a dozen glyphs to say what two do. Both are
 *   mono, so they carry tabular figures and a slashed zero (§2.5) and a
 *   ticking clock cannot change their width.
 * - **The headline number sits above the plot and the breakdown right-aligns
 *   on the same line.** The eye reads the conclusion, then the detail, and
 *   both arrive before the marks do.
 * - **The legend goes below the plot, never in a corner box inside it.** A
 *   floating legend covers data and adds a frame; below the plot it covers
 *   nothing. What goes in it is the caller's business — this component only
 *   guarantees there is a place for it.
 */

export type ChartProps = {
  /** The number the chart is about. Rendered large, above the plot. */
  headline: ReactNode;
  /** The split behind the headline, right-aligned on the headline's line. */
  breakdown?: ReactNode;
  /** Start of the window, shown in the bottom-left corner. */
  start?: string;
  /** End of the window, shown in the bottom-right corner. */
  end?: string;
  /**
   * How many gridlines to draw behind the plot. Three is enough to judge a
   * height against; more turns the background into a texture.
   */
  gridLines?: number;
  /**
   * The legend slot, below the plot. Deliberately a slot rather than a list of
   * items: which marks need naming is something only the caller knows, and a
   * legend built in here would be a second legend component in the product.
   */
  legend?: ReactNode;
  /** The plot itself — the marks, drawn by the caller. */
  children: ReactNode;
  className?: string;
};

export function Chart({
  headline,
  breakdown,
  start,
  end,
  gridLines = 3,
  legend,
  children,
  className,
}: ChartProps) {
  // Evenly spaced across the plot, and never on its top or bottom edge: a line
  // sitting exactly on the baseline reads as an axis, which is the one thing
  // this chrome refuses to draw.
  const lines = Array.from(
    { length: Math.max(gridLines, 0) },
    (_, index) => ((index + 1) / (Math.max(gridLines, 0) + 1)) * 100,
  );
  const hasRange = start !== undefined || end !== undefined;

  return (
    <div className={className ? `chart ${className}` : "chart"}>
      <div className="chart-head">
        <span className="chart-headline" data-testid="chart-headline">
          {headline}
        </span>
        {breakdown !== undefined && (
          <span className="chart-breakdown" data-testid="chart-breakdown">
            {breakdown}
          </span>
        )}
      </div>

      <div className="chart-plot" data-testid="chart-plot">
        {/* Decorative by definition: the gridlines carry no value a reader
            could name, and the plot's own content states the numbers. */}
        <div className="chart-grid" aria-hidden="true" data-testid="chart-grid">
          {lines.map((top) => (
            <span
              className="chart-gridline"
              key={top}
              style={{ top: `${top}%` }}
            />
          ))}
        </div>
        <div className="chart-marks">{children}</div>
      </div>

      {hasRange && (
        <div className="chart-range">
          {/* Both corners are always rendered, even when one time is missing,
              so the remaining one stays anchored to its own end instead of
              sliding to the middle. */}
          <span className="chart-time" data-testid="chart-start">
            {start}
          </span>
          <span className="chart-time" data-testid="chart-end">
            {end}
          </span>
        </div>
      )}

      {legend !== undefined && (
        <div className="chart-legend" data-testid="chart-legend">
          {legend}
        </div>
      )}
    </div>
  );
}
