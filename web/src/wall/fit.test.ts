import { describe, expect, it } from "vitest";
import { fitZoom, zoomCeiling } from "./fit";

describe("zoomCeiling", () => {
  it("gives a 1080p screen 2x and a 4K one 4x", () => {
    expect(zoomCeiling(1920, 1080)).toBe(2);
    expect(zoomCeiling(3840, 2160)).toBe(4);
  });

  it("takes the tighter of the two sides", () => {
    // 2560 x 1600 is tall for its width: width allows 2.67, height 2.96.
    expect(zoomCeiling(2560, 1600)).toBeCloseTo(2.667, 3);
    // An ultrawide is the other way round.
    expect(zoomCeiling(3440, 1440)).toBeCloseTo(2.667, 3);
  });

  it("never shrinks the board, so a phone draws the wall as before", () => {
    expect(zoomCeiling(900, 600)).toBe(1);
    expect(zoomCeiling(390, 844)).toBe(1);
    expect(zoomCeiling(320, 568)).toBe(1);
  });
});

describe("fitZoom", () => {
  /** A board that fits up to `limit`, the shape the real one has. */
  const upTo = (limit: number) => (zoom: number) => zoom <= limit;

  it("finds the largest factor that fits, to within a hundredth", () => {
    const zoom = fitZoom(upTo(1.91), 3);
    expect(zoom).toBeLessThanOrEqual(1.91);
    expect(zoom).toBeGreaterThan(1.9);
  });

  it("keeps that precision on a large screen's wide range", () => {
    // An 8K panel allows 8x: the search must not get coarser with the range.
    const zoom = fitZoom(upTo(7.43), 8);
    expect(zoom).toBeLessThanOrEqual(7.43);
    expect(zoom).toBeGreaterThan(7.42);
  });

  it("stops at the ceiling when everything fits", () => {
    expect(fitZoom(upTo(10), 3)).toBe(3);
  });

  it("stays at 1 when even 1 does not fit, rather than shrinking", () => {
    expect(fitZoom(upTo(0.5), 3)).toBe(1);
  });

  it("does not search when the ceiling is 1", () => {
    let probes = 0;
    expect(
      fitZoom(() => {
        probes += 1;
        return true;
      }, 1),
    ).toBe(1);
    expect(probes).toBe(0);
  });

  it("returns a factor that fits, never one just past the limit", () => {
    for (const limit of [1.0001, 1.5, 2.4999, 2.9999]) {
      const fits = upTo(limit);
      expect(fits(fitZoom(fits, 3)), String(limit)).toBe(true);
    }
  });
});
