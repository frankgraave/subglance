import type { MonitorStatus } from "./types";
import { LED_LABELS, LED_STATE } from "./ledState";

/**
 * The status lamp (DESIGN.md §3) — the product's brand mark.
 *
 * One size everywhere, on purpose. A lamp that grows in a card and shrinks in
 * a row stops being an instrument and becomes decoration; keeping 20x7 fixed
 * is what makes a wall of them scannable.
 *
 * The status-to-state mapping lives in `ledState.ts` rather than here: the
 * toolbar's filter chips draw a small round key in the same colours and need
 * the table without needing the component, and a component module that also
 * exports constants breaks fast refresh.
 */

export type { LedState } from "./ledState";

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

export function Led({
  status,
  labelled = true,
  hideLabel = true,
  className,
}: LedProps) {
  const label = LED_LABELS[status];
  const lamp = (
    <span
      className="led"
      data-state={LED_STATE[status]}
      data-status={status}
      aria-hidden="true"
    />
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
