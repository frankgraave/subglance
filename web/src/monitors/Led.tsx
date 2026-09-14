import type { MonitorStatus } from "./types";

/**
 * The status lamp (DESIGN.md §3) — the product's brand mark.
 *
 * One size everywhere, on purpose. A lamp that grows in a card and shrinks in
 * a row stops being an instrument and becomes decoration; keeping 20x7 fixed
 * is what makes a wall of them scannable.
 */

/** How each status is spoken. Colour never stands alone (DESIGN.md §2.3). */
const LABELS: Record<MonitorStatus, string> = {
  up: "Up",
  down: "Down",
  pending: "Pending",
  paused: "Paused",
  waiting: "Waiting",
};

/**
 * The lamp's visual states.
 *
 * `idle` is a filled but unlit grey lamp: we have no reading. `off` is the
 * same silhouette with nothing in it: nobody is taking a reading, on purpose.
 * They are two states rather than one colour because they are two different
 * facts, and the shape — not the hue — is what separates them (DESIGN.md §3).
 */
export type LedState = "up" | "down" | "warn" | "idle" | "off";

/** Status to lamp state. */
const STATE: Record<MonitorStatus, LedState> = {
  up: "up",
  down: "down",
  // Pending is amber, not grey: it is a monitor we are waiting on, which is
  // worth a glance.
  pending: "warn",
  // Paused is hollow, not grey-filled. A grey fill is what "no reading yet"
  // looks like, and a paused monitor is not waiting for a reading — it was
  // switched off by a person. Sharing one signal meant a monitor someone
  // paused by accident was indistinguishable from one that had just started
  // (DESIGN.md rule 4).
  paused: "off",
  // Grey and filled: the lamp for a monitor with no reading at all. That is
  // exactly what a push monitor with no report yet is, and it is not amber —
  // amber means a check is in flight, and no check is running here. Nor is it
  // hollow: nobody switched this off, it has simply never been pinged.
  waiting: "idle",
};

export type LedProps = {
  status: MonitorStatus;
  /**
   * Renders the lamp's text alternative as a real label.
   *
   * Default `true`: a bare coloured pill is meaningless to a screen reader and
   * to anyone who cannot separate red from green. Pass `false` only where the
   * surrounding markup already says the status in words — an unlabelled lamp
   * next to the word "Down" is noise, not accessibility.
   */
  labelled?: boolean;
  /** Visually hides the label while keeping it for assistive technology. */
  hideLabel?: boolean;
  className?: string;
};

export function Led({ status, labelled = true, hideLabel = true, className }: LedProps) {
  const label = LABELS[status];
  const lamp = (
    <span className="led" data-state={STATE[status]} data-status={status} aria-hidden="true" />
  );

  if (!labelled) {
    // Purely decorative: the status is already in the accessible name of
    // something nearby, so the lamp is hidden rather than repeated.
    return <span className={className}>{lamp}</span>;
  }

  return (
    <span className={className ? `led-wrap ${className}` : "led-wrap"}>
      {lamp}
      <span className={hideLabel ? "sr-only" : "led-label"}>{label}</span>
    </span>
  );
}
