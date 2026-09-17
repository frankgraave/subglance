/**
 * The small glyph set used by card headers and the toolbar.
 *
 * Inline rather than an icon package: a dozen shapes at one size do not justify
 * a dependency, and every one of them is drawn on the same 24-unit grid with the
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
 * The three row actions that become glyphs on the inventory (SUB-134).
 *
 * Drawn on the same 24-unit grid at the same stroke as the header icons above,
 * so a row of them sits at one optical weight. They are decorative by
 * construction: each one is rendered inside a button whose `aria-label`
 * carries the verb AND the monitor's name, because forty buttons called
 * "Pause" is a list a screen reader cannot navigate and a voice-control user
 * cannot address.
 *
 * Delete deliberately has no glyph here. It stays a word — see
 * MonitorInventoryRow for the argument.
 */

/** Check now: an arrow completing a circle. */
export function IconRefresh() {
  return (
    <svg {...BASE}>
      <path d="M20 12a8 8 0 1 1-2.6-5.9" />
      <path d="M20 4v4h-4" />
    </svg>
  );
}

/** Pause: two bars. Stroked like the rest rather than filled, so it carries
    the same weight as the glyphs beside it. */
export function IconPause() {
  return (
    <svg {...BASE}>
      <path d="M9.5 5.5v13M14.5 5.5v13" />
    </svg>
  );
}

/** Resume: a play triangle. The counterpart to pause, and a different shape
    rather than the same shape in a different colour — the button's state has
    to survive greyscale (DESIGN.md §2.3). */
export function IconPlay() {
  return (
    <svg {...BASE}>
      <path d="M8 5.5 19 12 8 18.5Z" />
    </svg>
  );
}

/** Edit: a pencil. */
export function IconPencil() {
  return (
    <svg {...BASE}>
      <path d="M4 20h4L19.5 8.5a2.1 2.1 0 0 0-3-3L5 17v3Z" />
      <path d="M14.5 5.5l4 4" />
    </svg>
  );
}

/**
 * Delete: a bin.
 *
 * Drawn on the same 24-unit grid at the same stroke as its neighbours, so the
 * destructive action is not also the odd shape in the row. What makes it read
 * as destructive is the silhouette — a lid, a body, two lines — rather than
 * the colour it is given: the row tints it, but a greyscale screen still
 * shows a bin (DESIGN.md §2.3).
 */
export function IconTrash() {
  return (
    <svg {...BASE}>
      <path d="M5 7h14" />
      <path d="M9.5 7V5.5a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1V7" />
      <path d="M6.5 7l.8 11a1.6 1.6 0 0 0 1.6 1.5h6.2a1.6 1.6 0 0 0 1.6-1.5L17.5 7" />
      <path d="M10.5 10.5v6M13.5 10.5v6" />
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
 * Theme glyphs: sun, crescent, and a circle half-filled for "follow the system".
 *
 * Same 24-unit grid as the rest of this file, at a 1.7 stroke rather than 1.5 —
 * the sun is mostly short rays, and a 1.5 hairline of that length disappears
 * beside the solid bars of the column glyphs in the same toolbar.
 *
 * ## The geometry is measured, not eyeballed
 *
 * A sun, a crescent and a half-filled disc are three very different amounts of
 * ink in the same box, and the first draft showed it: rasterised at the 16px
 * these actually render at, the auto disc carried 1.87x the ink of the sun and
 * read as the selected one no matter which segment was pressed.
 *
 * The numbers below come from sweeping sun radius, ray length, crescent size
 * and disc radius, rasterising each combination at 16px and scoring it on the
 * ratio between the heaviest and lightest glyph. This set measures 1.06 —
 * 864/904/918 coverage units. Change one of them and the row tilts again, so
 * change them together and re-measure.
 */
const THEME = { ...BASE, strokeWidth: 1.7 };

/** Light: a sun. */
export function IconSun() {
  return (
    <svg {...THEME}>
      <circle cx="12" cy="12" r="5" />
      {/* Eight separate rays rather than a dashed circle: a dash array is
          relative to the path length, so it would re-space itself at every
          size instead of holding the 4-unit ray this is tuned to. */}
      <path d="M12 1.8v4M12 18.2v4M22.2 12h-4M5.8 12h-4" />
      <path d="M19.21 4.79 16.38 7.62M7.62 16.38 4.79 19.21M19.21 19.21 16.38 16.38M7.62 7.62 4.79 4.79" />
    </svg>
  );
}

/**
 * Dark: a crescent.
 *
 * One filled path rather than a circle with a circle punched out of it. The
 * subtractive version needs the cut-out painted in the button background,
 * which is `--accent` when the segment is selected and transparent when it is
 * not — so the glyph would have to know which state it is in. A crescent that
 * is its own shape works in both.
 */
export function IconMoon() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <path d="M19.98 14.52A8.4 8.4 0 0 1 9.48 4.02a8.4 8.4 0 1 0 10.5 10.5Z" />
    </svg>
  );
}

/**
 * Auto: one circle, half of it filled.
 *
 * The usual glyph for "follow the operating system" is a computer display.
 * It is wrong *here*: in SubGlance a monitor is a check on a target, and a
 * screen-shaped button in the toolbar of a monitoring tool reads as one more
 * thing about monitors. The half-filled circle says light-and-dark-at-once
 * without borrowing a noun the product has already spent.
 *
 * The outline is stroked separately from the fill so the circle keeps a full
 * edge — a filled half-disc alone is a shape with one straight side, which at
 * 16px reads as a chipped dot rather than as a divided circle.
 */
export function IconThemeAuto() {
  return (
    <svg {...THEME}>
      <circle cx="12" cy="12" r="6.8" />
      <path d="M12 5.2a6.8 6.8 0 0 0 0 13.6Z" fill="currentColor" stroke="none" />
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
