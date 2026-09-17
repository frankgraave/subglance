import type { ReactNode } from "react";
import { StateChip, type ChipStatus } from "./Chip";

/**
 * The readout tooltip (DESIGN.md §8.5).
 *
 * A tooltip in this product is a small table, not a sentence. The reference
 * style's structure, and the reason each part earns its place:
 *
 * - **A mono timestamp header, with the unit beside it.** The unit belongs in
 *   the header rather than repeated on every row, because a tooltip whose rows
 *   all end in "ms" spends its width saying the same word three times.
 * - **A full-width divider** under the header, so the header reads as a
 *   caption for the rows rather than as the first of them.
 * - **One row per series**: colour marker, mono caps label, the status in
 *   words, right-aligned value. Right alignment is what makes a column of
 *   numbers comparable at a glance; the mono role carries the tabular figures
 *   (§2.5). The status is spelled out because the marker is decorative and
 *   carries its meaning in hue alone, which is no meaning at all in greyscale,
 *   to a screen reader, or to a red-green colour-blind reader.
 * - **A closing TOTAL row**, separated by its own rule.
 * - **A dashed PARTIAL DATA chip** when the bucket is incomplete.
 *
 * That last part is the reason this component exists now rather than later. A
 * heartbeat bucket holding two of its forty checks was drawn exactly like a
 * complete one, so a gap in the history read as a healthy stretch — the one
 * thing a monitoring tool must never do. Dashed is already the product's
 * signal for "about the data" (§8.1), so the chip says it without a legend.
 */

export type TooltipRow = {
  /** Stable identity for the row. */
  key: string;
  /** Row label. Rendered in mono caps, so it is written in words, not glyphs. */
  label: string;
  value: ReactNode;
  /**
   * Status colour for the row's marker. Omit for a row that names no status —
   * the marker is then left out rather than drawn in a neutral, because a
   * colourless dot beside a coloured one reads as a status of its own.
   */
  marker?: ChipStatus;
  /**
   * The same status in words, e.g. "Up" or "Down". Required whenever `marker`
   * is set: the marker is `aria-hidden` and differs from its neighbours only
   * by hue, so on its own it states the row's status in colour alone.
   */
  status?: string;
};

export type TooltipProps = {
  /** Mono timestamp or range heading the readout. */
  timestamp: string;
  /** Unit for every value below, named once beside the timestamp. */
  unit?: string;
  rows: TooltipRow[];
  /** Closing summary row, set apart by its own rule. */
  total?: { label: string; value: ReactNode };
  /**
   * True when the readout covers fewer checks than its window should hold.
   * Draws the dashed PARTIAL DATA chip.
   */
  partial?: boolean;
  /** Free-form line below the rows, e.g. the failure reason. */
  footer?: ReactNode;
  className?: string;
};

export function Tooltip({
  timestamp,
  unit,
  rows,
  total,
  partial = false,
  footer,
  className,
}: TooltipProps) {
  return (
    <div className={className ? `tooltip ${className}` : "tooltip"}>
      <div className="tooltip-head">
        <span className="tooltip-time">{timestamp}</span>
        {unit && <span className="tooltip-unit">{unit}</span>}
      </div>

      {rows.length > 0 && (
        <div className="tooltip-rows">
          {rows.map((row) => (
            <div className="tooltip-row" key={row.key}>
              {row.marker ? (
                <span
                  className="tooltip-marker"
                  data-status={row.marker}
                  aria-hidden="true"
                />
              ) : (
                <span className="tooltip-marker-gap" aria-hidden="true" />
              )}
              <span className="tooltip-label">{row.label}</span>
              <span className="tooltip-status">{row.status}</span>
              <span className="tooltip-value">{row.value}</span>
            </div>
          ))}
        </div>
      )}

      {total && (
        <div className="tooltip-row tooltip-row--total">
          <span className="tooltip-marker-gap" aria-hidden="true" />
          <span className="tooltip-label">{total.label}</span>
          <span className="tooltip-status" />
          <span className="tooltip-value">{total.value}</span>
        </div>
      )}

      {footer && <div className="tooltip-footer">{footer}</div>}

      {partial && (
        <div className="tooltip-partial">
          <StateChip>Partial data</StateChip>
        </div>
      )}
    </div>
  );
}
