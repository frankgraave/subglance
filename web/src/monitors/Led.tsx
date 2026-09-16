import type { MonitorStatus } from "./types";
import { statusWord } from "./format";
import { LED_STATE } from "./ledState";

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
  /**
   * True when the live stream is dead and this reading is history.
   *
   * It changes the *word*, not the lamp: "Was up" instead of "Up". The lamp's
   * own withdrawal is CSS on `[data-conn="stale"]` (see connection.css), and
   * that is precisely why this prop has to exist — a stylesheet cannot reach
   * the label, and on every list layout the label is `sr-only`. Draining the
   * colour while the hidden text still says "Up" would fix the lie for sighted
   * readers and leave it standing for everyone using a screen reader, which is
   * the reverse of who DESIGN.md §2.3 exists for.
   */
  stale?: boolean;
  className?: string;
};

export function Led({
  status,
  labelled = true,
  hideLabel = true,
  stale = false,
  className,
}: LedProps) {
  const label = statusWord(status, stale);
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
