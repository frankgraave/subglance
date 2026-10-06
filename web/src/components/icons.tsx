/**
 * The small glyph set used by card headers and the toolbar.
 *
 * Inline rather than an icon package: a dozen shapes at one size do not justify
 * a dependency, and every one of them is drawn on the same 24-unit grid with the
 * same 1.5 stroke so they sit at one optical weight inside a tile.
 *
 * They are `aria-hidden` here, on the `<svg>` itself, rather than only by way
 * of `IconTile`. The tile was the only consumer when that was written; the
 * dashboard toolbar now places one of these directly inside a `<label>`, where
 * a wrapper would have to remember. What a screen reader makes of an unnamed
 * inline `<svg>` is not fixed — some skip it, some announce "graphic" — so the
 * decision belongs on the element rather than on whoever holds it.
 */

const BASE = {
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.5,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  "aria-hidden": true,
  focusable: false,
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

/**
 * A tag, for a filter that narrows the list by one of a monitor's tag keys.
 *
 * The same glyph for every facet on purpose: `env`, `team` and `customer` are
 * one kind of control, and giving each its own picture would ask the reader to
 * learn three symbols for one idea. The key's own word beside it is what tells
 * them apart — the icon says "this narrows the list", the label says by what.
 */
export function IconTag() {
  return (
    <svg {...BASE}>
      <path d="M11.6 3.5H4.5a1 1 0 0 0-1 1v7.1a1 1 0 0 0 .3.7l8.4 8.4a1 1 0 0 0 1.4 0l7.1-7.1a1 1 0 0 0 0-1.4L12.3 3.8a1 1 0 0 0-.7-.3Z" />
      <path d="M7.8 7.8h.01" />
    </svg>
  );
}

/**
 * Grouping: rows gathered under headings.
 *
 * Deliberately *not* the tag glyph. Group by sits beside the facet filters and
 * answers a neighbouring question about the same tags, but it does not narrow
 * anything — and a control that looks like a filter while changing nothing
 * about what is visible is the kind of thing people press twice.
 */
export function IconGroup() {
  return (
    <svg {...BASE}>
      <path d="M3 5h8M3 12h8M3 19h8" />
      <path d="M15 5h6M15 12h6M15 19h6" />
    </svg>
  );
}

/**
 * View: a frame split into panes, for the dashboard's View button — how the
 * list is drawn and arranged, never which monitors are in it. Not the column
 * glyphs: those are the options inside the panel, and the button that opens
 * it should not look like one of them.
 */
export function IconLayout() {
  return (
    <svg {...BASE}>
      <rect x="3.5" y="4.5" width="17" height="15" rx="1.5" />
      <path d="M3.5 10h17M10 10v9.5" />
    </svg>
  );
}

/**
 * A funnel, for a toolbar filter that narrows the list by something other
 * than a tag: a monitor's type, whether it is paused, whether an incident is
 * open.
 *
 * Not the tag glyph, because a tag filter's picture says what it narrows by,
 * and a type is not a tag. Still one glyph for every such filter, for the
 * reason `IconTag` gives: one kind of control, one picture, and the key beside
 * it says by what.
 */
export function IconFilter() {
  return (
    <svg {...BASE}>
      <path d="M3.5 5h17l-6.5 7.5V19l-4 1.5v-8L3.5 5Z" />
    </svg>
  );
}

/**
 * Order: a long bar over shorter ones, for a toolbar control that changes
 * where rows sit and never which rows are shown.
 *
 * Its own glyph for the reason `IconGroup` has one: a control that looks like
 * a filter while hiding nothing is the kind of thing people press twice.
 */
export function IconSort() {
  return (
    <svg {...BASE}>
      <path d="M4 6h16M4 12h11M4 18h6" />
    </svg>
  );
}

/** Accounts: two heads and shoulders, for a card that lists people. */
export function IconUsers() {
  return (
    <svg {...BASE}>
      <circle cx="9" cy="8" r="3.5" />
      <path d="M2.5 19.5a6.5 6.5 0 0 1 13 0" />
      <path d="M15.5 4.8a3.5 3.5 0 0 1 0 6.4" />
      <path d="M18 14.2a6.5 6.5 0 0 1 3.5 5.3" />
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
 * One glyph per settings card and per remaining screen card (SUB-167).
 *
 * Every card carries an icon tile, so every card needs a glyph that says what
 * it is about. Each is drawn on the same 24-unit grid and 1.5 stroke as the
 * ones above. They name the subject, not an action: the tile is a mark you
 * read, and a verb-shaped glyph there would look like a button.
 */

/** Display: a screen on a stand, for how this browser shows the app. */
export function IconDisplay() {
  return (
    <svg {...BASE}>
      <rect x="3" y="4.5" width="18" height="12" rx="1.5" />
      <path d="M9 20.5h6M12 16.5v4" />
    </svg>
  );
}

/** Status pages: a globe, for what the instance tells people without an account. */
export function IconGlobe() {
  return (
    <svg {...BASE}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M3.5 12h17" />
      <path d="M12 3.5c2.3 2.4 3.5 5.3 3.5 8.5s-1.2 6.1-3.5 8.5c-2.3-2.4-3.5-5.3-3.5-8.5s1.2-6.1 3.5-8.5Z" />
    </svg>
  );
}

/** Retention and storage: a database drum. */
export function IconDatabase() {
  return (
    <svg {...BASE}>
      <ellipse cx="12" cy="6" rx="7.5" ry="2.5" />
      <path d="M4.5 6v12c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5V6" />
      <path d="M4.5 12c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5" />
    </svg>
  );
}

/** Backups: an archive box, for a copy kept somewhere else. */
export function IconArchive() {
  return (
    <svg {...BASE}>
      <rect x="3.5" y="4" width="17" height="4.5" rx="1" />
      <path d="M5 8.5V19a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8.5" />
      <path d="M10 12.5h4" />
    </svg>
  );
}

/** Import and export: one arrow out, one arrow in. */
export function IconTransfer() {
  return (
    <svg {...BASE}>
      <path d="M8 19.5v-15M4 8.5l4-4 4 4" />
      <path d="M16 4.5v15M12 15.5l4 4 4-4" />
    </svg>
  );
}

/** API tokens: a key. */
export function IconKey() {
  return (
    <svg {...BASE}>
      <circle cx="8" cy="15.5" r="4.5" />
      <path d="M11.2 12.3 20 3.5" />
      <path d="M16.5 7l2.5 2.5M14 9.5l2 2" />
    </svg>
  );
}

/** The instance itself: two stacked server units. */
export function IconServer() {
  return (
    <svg {...BASE}>
      <rect x="3.5" y="4" width="17" height="7" rx="1.5" />
      <rect x="3.5" y="13" width="17" height="7" rx="1.5" />
      <path d="M7.5 7.5h.01M7.5 16.5h.01" />
    </svg>
  );
}

/**
 * The connectivity check: this host above the addresses it dials, for a card
 * about the instance's own uplink rather than about any monitor.
 */
export function IconNetwork() {
  return (
    <svg {...BASE}>
      <rect x="9" y="3.5" width="6" height="5" rx="1" />
      <path d="M12 8.5v4M6 12.5h12M6 12.5v3M18 12.5v3" />
      <rect x="3" y="15.5" width="6" height="5" rx="1" />
      <rect x="15" y="15.5" width="6" height="5" rx="1" />
    </svg>
  );
}

/**
 * Failure responses: a page of text, for the bodies a failing check returned.
 *
 * Not the warning triangle, which heads Incidents on the same monitor screen:
 * the responses are evidence about a failure, and the card holds documents.
 */
export function IconResponse() {
  return (
    <svg {...BASE}>
      <path d="M14 3.5H6.5a1 1 0 0 0-1 1v15a1 1 0 0 0 1 1h11a1 1 0 0 0 1-1V8Z" />
      <path d="M14 3.5V8h4.5" />
      <path d="M9 12.5h6M9 16h6" />
    </svg>
  );
}

/**
 * Channels: a paper plane, for where an alert is sent.
 *
 * Not the bell: the bell already heads "Who hears what" on the same screen,
 * and two cards with one glyph side by side read as one subject twice.
 */
export function IconSend() {
  return (
    <svg {...BASE}>
      <path d="M20.5 3.5 10.5 13.5" />
      <path d="M20.5 3.5 14 20.5l-3.5-7-7-3.5 17-6.5Z" />
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
 * Delete has a glyph too, and this comment used to say the opposite: it was
 * the one action kept as a word, and SUB-138 replaced the word with the bin
 * for both lists. A bin is destructive in its silhouette, so the meaning
 * survives greyscale without the word, and the pause the word was buying is
 * bought properly by the confirmation that makes you retype the name.
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

/**
 * Mute a channel: a bell with a stroke through it.
 *
 * Not `IconPause`, though the row action is structurally the same toggle a
 * monitor has. Pausing a monitor stops SubGlance *checking* something;
 * disabling a channel leaves every check running and stops SubGlance
 * *telling* anyone. Drawing both with the same two bars would say those are
 * the same act, and the difference between them is the whole reason a
 * self-hoster would ever pick one over the other.
 *
 * The slash is a shape, not a colour, so "muted" survives greyscale — and it
 * is the opposite silhouette from the plain bell rather than the same one
 * tinted (DESIGN.md §2.3).
 */
export function IconBellOff() {
  return (
    <svg {...BASE}>
      <path d="M9 17a3 3 0 0 0 6 0" />
      <path d="M6.2 9.8A5.8 5.8 0 0 1 12 4a5.8 5.8 0 0 1 5.8 5.8c0 4 .8 5.6 1.5 6.4H5.1c.4-.5.8-1.3 1-2.6" />
      <path d="M4 4l16 16" />
    </svg>
  );
}

/** Unmute a channel: the same bell without the stroke. */
export function IconBell() {
  return (
    <svg {...BASE}>
      <path d="M9 17a3 3 0 0 0 6 0" />
      <path d="M6.2 9.8A5.8 5.8 0 0 1 12 4a5.8 5.8 0 0 1 5.8 5.8c0 4 .8 5.6 1.5 6.4H4.7c.7-.8 1.5-2.4 1.5-6.4Z" />
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
