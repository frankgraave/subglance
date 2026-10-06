import { useLayoutEffect, useRef, type RefObject } from "react";
import { fitZoom, zoomCeiling } from "./fit";

/**
 * Magnifies the wall's board to fill the screen, and again on every resize
 * (see `fit.ts` for why, and for the bounds).
 *
 * The factor is found by measuring, not by arithmetic over the grid: the
 * board's height depends on how many columns the grid makes at each width,
 * how the header line wraps and what the fonts measure, and only the browser
 * knows those. Each probe writes `zoom` on the board and reads its height
 * back, about a dozen layouts per fit, and a fit only runs when the screen or
 * what the board holds changes — never on a heartbeat.
 *
 * The factor goes straight onto the element rather than through React state:
 * it is a measurement of the layout React just produced, and putting it in
 * state would render the whole wall a second time to apply it.
 *
 * `key` is whatever changes the board's shape: the monitor count and the
 * text of the header line. A status change does neither, and a tile's name
 * is one line cut with an ellipsis, so neither needs a refit.
 */
export function useWallZoom(key: string): {
  /** The magnified element: the header line and the tiles. */
  board: RefObject<HTMLDivElement | null>;
  /** The frame around it, whose padding is not magnified. */
  stage: RefObject<HTMLDivElement | null>;
} {
  const board = useRef<HTMLDivElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const fit = () => {
      const el = board.current;
      const frame = stage.current;
      if (!el || !frame) return;
      el.style.zoom = "1";
      // No layout at all (jsdom, a hidden tab): nothing to measure against.
      if (el.getBoundingClientRect().height === 0) return;
      const style = getComputedStyle(frame);
      const room =
        window.innerHeight -
        Number.parseFloat(style.paddingTop) -
        Number.parseFloat(style.paddingBottom);
      const root = document.documentElement;
      const fits = (zoom: number) => {
        el.style.zoom = String(zoom);
        return (
          el.getBoundingClientRect().height <= room &&
          root.scrollWidth <= root.clientWidth
        );
      };
      el.style.zoom = String(
        fitZoom(fits, zoomCeiling(window.innerWidth, window.innerHeight)),
      );
    };
    fit();
    // The web fonts may land after the first fit, and a name measured in the
    // fallback face is a different width; fit again once they have.
    let live = true;
    void document.fonts?.ready.then(() => {
      if (live) fit();
    });
    window.addEventListener("resize", fit);
    return () => {
      live = false;
      window.removeEventListener("resize", fit);
    };
  }, [key]);
  return { board, stage };
}
