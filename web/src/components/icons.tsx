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

/**
 * Column-count glyphs: N filled bars in the same 24-unit box.
 *
 * Filled rather than stroked, which is the one deviation from `BASE` in this
 * file and is deliberate: at 3 columns a stroked outline is two hairlines 2px
 * apart and reads as noise rather than as a bar. The shape *is* the meaning
 * here — the button shows the layout it selects instead of naming it — so it
 * is drawn as blocks, and the count is legible at a glance.
 *
 * Every caller pairs these with a `title`, because "two bars" is only obvious
 * once you already know what the control does.
 */
function columnBars(count: number) {
  // One 24-wide box, `count` bars, 3 units of gap. Solving for the width keeps
  // the glyph optically the same weight at every count instead of leaving 1
  // column as a lonely sliver or 3 as a solid block.
  const gap = 3;
  const width = (24 - gap * (count - 1)) / count;
  return Array.from({ length: count }, (_, i) => (
    <rect
      // Index is the identity here: these are N interchangeable bars in a
      // fixed row, not data with a key of its own.
      key={i}
      x={i * (width + gap)}
      y={4}
      width={width}
      height={16}
      rx={1.5}
    />
  ));
}

/** One card per row. */
export function IconColumnsOne() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      {columnBars(1)}
    </svg>
  );
}

/** Two cards per row. */
export function IconColumnsTwo() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      {columnBars(2)}
    </svg>
  );
}

/** Three cards per row. */
export function IconColumnsThree() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      {columnBars(3)}
    </svg>
  );
}

/**
 * Fill the width: three bars where the last one is cut off by the frame.
 *
 * The clipped bar is the whole idea — the count is not fixed, it runs to
 * whatever fits — and it is what distinguishes this from the 3 glyph beside
 * it without falling back to the letter A.
 */
export function IconColumnsAuto() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <rect x={0} y={4} width={6} height={16} rx={1.5} />
      <rect x={9} y={4} width={6} height={16} rx={1.5} />
      <rect x={18} y={4} width={3} height={16} rx={1.5} opacity={0.45} />
    </svg>
  );
}
