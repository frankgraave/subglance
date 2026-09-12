import { describe, expect, it } from "vitest";
import {
  barHeight,
  DOWN_HEIGHT,
  latencyCeiling,
  MAX_OK_HEIGHT,
  MIN_OK_HEIGHT,
  slotCountFor,
  summarise,
  toSlots,
  tooltipLeft,
  UNKNOWN_LATENCY_HEIGHT,
  type Beat,
  type BeatSlot,
} from "./model";

const beat = (i: number, over: Partial<Beat> = {}): Beat => ({
  ts: 1_700_000_000_000 + i * 60_000,
  ok: true,
  latencyMs: 100,
  ...over,
});

const beats = (n: number, over: (i: number) => Partial<Beat> = () => ({})) =>
  Array.from({ length: n }, (_, i) => beat(i, over(i)));

describe("slotCountFor", () => {
  it("fits bars plus gaps, and never returns a negative count", () => {
    expect(slotCountFor(100, 6, 3)).toBe(11); // 11*9 - 3 = 96 <= 100
    expect(slotCountFor(0, 6, 3)).toBe(0);
    expect(slotCountFor(-40, 6, 3)).toBe(0);
  });
});

describe("toSlots", () => {
  it("pads on the left when history is shorter than the track", () => {
    const slots = toSlots(beats(3), 10);
    expect(slots).toHaveLength(10);
    expect(slots.slice(0, 7).every((s) => s.kind === "empty")).toBe(true);
    expect(slots[9].kind).toBe("beat");
    // Newest beat is on the right.
    expect((slots[9] as BeatSlot).to).toBe(beat(2).ts);
  });

  it("buckets a long series into exactly the available columns", () => {
    const slots = toSlots(beats(500), 40);
    expect(slots).toHaveLength(40);
    const total = slots.reduce((sum, s) => sum + (s.kind === "beat" ? s.count : 0), 0);
    expect(total).toBe(500);
    // Newest column is the finest-grained one.
    expect((slots[39] as BeatSlot).count).toBeLessThanOrEqual((slots[0] as BeatSlot).count);
  });

  it("never averages a failure away: one bad check makes the bucket bad", () => {
    const series = beats(100, (i) => (i === 3 ? { ok: false, error: "500", latencyMs: 12 } : {}));
    const slots = toSlots(series, 10);
    const failing = slots.filter((s) => s.kind === "beat" && !s.ok);
    expect(failing).toHaveLength(1);
    expect((failing[0] as BeatSlot).error).toBe("500");
    expect((failing[0] as BeatSlot).downCount).toBe(1);
  });

  it("renders an all-empty track when there are no beats at all", () => {
    expect(toSlots([], 5).every((s) => s.kind === "empty")).toBe(true);
    expect(toSlots(beats(10), 0)).toEqual([]);
  });
});

describe("latencyCeiling", () => {
  it("ignores a single extreme outlier instead of flattening the rest", () => {
    const normal = toSlots(beats(40, () => ({ latencyMs: 100 })), 40);
    const withSpike = toSlots(
      beats(40, (i) => ({ latencyMs: i === 39 ? 40_000 : 100 })),
      40,
    );
    expect(latencyCeiling(normal)).toBe(100);
    // p95 of 40 samples is still 100, so the ordinary bars keep their height.
    expect(latencyCeiling(withSpike)).toBe(100);
    expect(barHeight(withSpike[0], latencyCeiling(withSpike))).toBeCloseTo(MAX_OK_HEIGHT, 6);
  });

  it("is 0 when nothing measurable exists, and heights fall back", () => {
    const slots = toSlots(beats(4, () => ({ latencyMs: null })), 4);
    expect(latencyCeiling(slots)).toBe(0);
    expect(barHeight(slots[0], 0)).toBe(UNKNOWN_LATENCY_HEIGHT);
  });

  it("excludes failures, whose height is fixed anyway", () => {
    const slots = toSlots(beats(4, (i) => (i === 3 ? { ok: false, latencyMs: 9000 } : {})), 4);
    expect(latencyCeiling(slots)).toBe(100);
  });
});

describe("barHeight", () => {
  it("draws every failure at full height, taller than any healthy bar", () => {
    const slots = toSlots(beats(2, (i) => (i === 0 ? { ok: false } : { latencyMs: 100_000 })), 2);
    const ceiling = latencyCeiling(slots);
    expect(barHeight(slots[0], ceiling)).toBe(DOWN_HEIGHT);
    expect(barHeight(slots[1], ceiling)).toBeLessThan(DOWN_HEIGHT);
    expect(MAX_OK_HEIGHT).toBeLessThan(DOWN_HEIGHT);
  });

  it("keeps a very fast check visible rather than zero-height", () => {
    const slots = toSlots(beats(2, (i) => ({ latencyMs: i === 0 ? 1 : 4000 })), 2);
    expect(barHeight(slots[0], latencyCeiling(slots))).toBeGreaterThanOrEqual(MIN_OK_HEIGHT);
  });

  it("gives an empty slot no height", () => {
    expect(barHeight(toSlots([], 1)[0], 100)).toBe(0);
  });
});

describe("summarise", () => {
  it("counts the underlying checks, not the drawn columns", () => {
    const slots = toSlots(beats(500, (i) => (i % 100 === 0 ? { ok: false } : {})), 40);
    expect(summarise(slots)).toMatchObject({ checks: 500, failed: 5 });
  });
});

describe("tooltipLeft", () => {
  const base = { trackLeft: 0, viewportWidth: 1000, tooltipWidth: 148 };

  it("centres the tooltip on its column when there is room", () => {
    expect(tooltipLeft({ ...base, columnCentre: 500 })).toBe(500 - 74);
  });

  it("overhangs a track narrower than the tooltip instead of squeezing into it", () => {
    // A 60px track in a wide window: clamping to the track would pin the
    // tooltip at 0 and cut it off; the viewport has room to spare.
    const left = tooltipLeft({ ...base, columnCentre: 57, trackLeft: 400 });
    expect(left).toBe(57 - 74);
  });

  it("stops at the left edge of the viewport", () => {
    expect(tooltipLeft({ ...base, columnCentre: 10, trackLeft: 4 })).toBe(8 - 4);
  });

  it("stops at the right edge of the viewport", () => {
    // Track starts at 900 in a 1000px window, so the tooltip may reach 1000-8.
    expect(tooltipLeft({ ...base, columnCentre: 80, trackLeft: 900 })).toBe(1000 - 8 - 148 - 900);
  });

  it("aligns to the left margin when the tooltip cannot fit at all", () => {
    expect(tooltipLeft({ ...base, columnCentre: 40, viewportWidth: 100 })).toBe(8);
  });
});
