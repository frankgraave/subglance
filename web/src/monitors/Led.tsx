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
};

/** Status to the four lamp colours the mockup's `data-state` defines. */
const STATE: Record<MonitorStatus, "up" | "down" | "warn" | "idle"> = {
  up: "up",
  down: "down",
  // Pending is amber, not grey: it is a monitor we are waiting on, which is
  // worth a glance. Paused is grey because it is a decision, not a condition.
  pending: "warn",
  paused: "idle",
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
