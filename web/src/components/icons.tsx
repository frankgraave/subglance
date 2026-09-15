/**
 * The small glyph set used by card headers.
 *
 * Inline rather than an icon package: five shapes at one size do not justify a
 * dependency, and every one of them is drawn on the same 24-unit grid with the
 * same 1.5 stroke so they sit at one optical weight inside a tile.
 *
 * All of them are `aria-hidden` by way of IconTile, which is where the
 * accessibility decision lives — a glyph next to a heading that already says
 * "Uptime" adds a word and no information.
 */

const BASE = {
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.5,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

/** Recent checks: a pulse line. */
export function IconPulse() {
  return (
    <svg {...BASE}>
      <path d="M3 12h4l3-7 4 14 3-7h4" />
    </svg>
  );
}

/** Uptime: a gauge. */
export function IconGauge() {
  return (
    <svg {...BASE}>
      <path d="M4 18a8 8 0 1 1 16 0" />
      <path d="M12 18l4-5" />
    </svg>
  );
}

/** Incidents: a warning triangle. */
export function IconAlert() {
  return (
    <svg {...BASE}>
      <path d="M12 4.5 2.8 19.5h18.4L12 4.5Z" />
      <path d="M12 10v4" />
      <path d="M12 17h.01" />
    </svg>
  );
}

/** A list of things. */
export function IconList() {
  return (
    <svg {...BASE}>
      <path d="M8 6h13M8 12h13M8 18h13" />
      <path d="M3 6h.01M3 12h.01M3 18h.01" />
    </svg>
  );
}

/** A clock, for anything about time windows. */
export function IconClock() {
  return (
    <svg {...BASE}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </svg>
  );
}
