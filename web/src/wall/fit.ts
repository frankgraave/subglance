/**
 * How far the status wall magnifies its board (SUB-199).
 *
 * The wall is read from across a room, on whatever screen is on that wall. At
 * the product's type scale a name is 15px: fine on a desk, unreadable on a
 * 1080p television a few metres away, and on a 4K panel it is half that
 * again. Raising the wall's own font sizes would not fix it, because no fixed
 * size is right for both a laptop and a 4K television; and it would take the
 * wall off the type scale every other screen is measured against.
 *
 * So the wall keeps the scale and magnifies the whole board — header, lamps,
 * names, gaps — with CSS `zoom`, by the largest factor at which the board
 * still fits the screen without scrolling. A wall has nobody at it to scroll,
 * so a board that runs off the bottom hides whatever is listed last.
 *
 * Fit alone would blow four monitors up to the size of a road sign, so the
 * factor has a ceiling: the board always lays out in at least 960 x 540 of its
 * own pixels. That is the reference size Android TV designs at and scales up
 * from, 2x on a 1080p screen and 4x on a 4K one, so the ceiling grows with the
 * screen exactly as the screen's own pixels shrink. At 2x a name is 30px on a
 * 1080p screen, between the 24px minimum and the 36px default that reference
 * gives for text read from about three metres. A desk monitor gets a little
 * (1.5x at 1440 x 900); below 960 x 540 the ceiling is 1, and a phone or a
 * small window draws the wall as it always has.
 * The floor is 1 as well: an estate too large for the screen keeps the
 * product's own sizes and scrolls, rather than shrinking names below the
 * scale's floor.
 */

/** The smallest box, in the board's own pixels, the wall lays out in. */
export const WALL_MIN_BOARD = { width: 960, height: 540 } as const;

/**
 * How close to the largest fitting factor the search gets. A two-hundredth
 * of a zoom is a pixel on a 200px-tall board, and about a dozen layouts on
 * any screen there is.
 */
const PRECISION = 0.005;

/** The largest magnification a screen of this size may give the board. */
export function zoomCeiling(width: number, height: number): number {
  return Math.max(
    1,
    Math.min(width / WALL_MIN_BOARD.width, height / WALL_MIN_BOARD.height),
  );
}

/**
 * The largest zoom in [1, ceiling] at which `fits` holds, to within
 * `PRECISION`, and always one at which it does hold.
 *
 * `fits` must be monotonic — true up to some factor, false above it — which
 * the board is: magnifying it makes it taller twice over, once by the factor
 * itself and once because a narrower layout box holds fewer columns.
 */
export function fitZoom(fits: (zoom: number) => boolean, ceiling: number): number {
  if (ceiling <= 1 || !fits(1)) return 1;
  if (fits(ceiling)) return ceiling;
  let lo = 1;
  let hi = ceiling;
  while (hi - lo > PRECISION) {
    const mid = (lo + hi) / 2;
    if (fits(mid)) lo = mid;
    else hi = mid;
  }
  return lo;
}
