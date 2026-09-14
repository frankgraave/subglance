import type { ReactNode } from "react";

/**
 * The chip family (DESIGN.md §8.1).
 *
 * Five kinds, one component, because the difference between them is what the
 * chip *says* and not how it is drawn. Before this they were three ad-hoc
 * spans in two files that had already drifted apart in padding and tone, and
 * a fourth was about to be hand-rolled for the heartbeat tooltip.
 *
 * The rule that holds them apart is stated once in `chips.css` and enforced by
 * the type here: a dashed edge means the chip is *about* the data — partial,
 * absent, unassigned — and a solid one means the chip *is* data.
 */

/** The four statuses a badge can carry. `pending` and `waiting` map to these
 *  at the call site, because a badge states a colour and those two share one. */
export type ChipStatus = "up" | "warn" | "down" | "idle";

export type StatusChipProps = {
  status: ChipStatus;
  /** The word on the badge. Written out, because colour never stands alone. */
  children: ReactNode;
  className?: string;
};

/** A status, filled with its own status colour and labelled in words. */
export function StatusChip({ status, children, className }: StatusChipProps) {
  return (
    <span
      className={join("chip chip--status", className)}
      data-status={status}
    >
      {children}
    </span>
  );
}

/** A number qualifying the control it sits in: "Rows 12". */
export function CountChip({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <span className={join("chip chip--count", className)}>{children}</span>;
}

/** A label and its value in one object, split by an internal divider. */
export function MetaChip({
  label,
  value,
  className,
}: {
  label: ReactNode;
  value: ReactNode;
  className?: string;
}) {
  return (
    <span className={join("chip chip--meta", className)}>
      <span className="chip-label">{label}</span>
      <span className="chip-value">{value}</span>
    </span>
  );
}

/**
 * A statement about the data rather than a reading of it — PARTIAL DATA, NOT
 * CHECKED YET. Dashed and never filled, so it cannot be mistaken for a status.
 */
export function StateChip({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <span className={join("chip chip--state", className)}>{children}</span>;
}

/**
 * A person-shaped hole: nobody is assigned.
 *
 * `role="img"` with a label rather than a bare decorative circle, because the
 * absence is the information — a screen reader that skips it gets a different
 * page from the one on screen.
 */
export function EmptyAvatar({
  label = "Nobody assigned",
  className,
}: {
  label?: string;
  className?: string;
}) {
  return (
    <span
      className={join("chip-avatar", className)}
      role="img"
      aria-label={label}
    />
  );
}

/**
 * A 32x32 neutral tile holding a glyph, anchoring the left of a card header.
 *
 * The glyph is hidden from assistive technology by default: a tile beside a
 * monitor's name is a visual anchor, and announcing "globe" before the name
 * adds a word that means nothing. Pass a `label` on the rare tile that is the
 * only thing saying what the row is.
 */
export function IconTile({
  children,
  label,
  className,
}: {
  children: ReactNode;
  label?: string;
  className?: string;
}) {
  return (
    <span
      className={join("icon-tile", className)}
      {...(label ? { role: "img", "aria-label": label } : { "aria-hidden": true })}
    >
      {children}
    </span>
  );
}

function join(base: string, extra?: string): string {
  return extra ? `${base} ${extra}` : base;
}
