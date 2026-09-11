import { describe, expect, it } from "vitest";
import { formatClock } from "./clock";

describe("the wall clock", () => {
  it("is 24-hour and zero-padded, so the width never jumps", () => {
    // Local time on purpose: the wall shows the time of the room it is in.
    const at = (h: number, m: number, s: number) =>
      formatClock(new Date(2026, 8, 11, h, m, s).getTime());
    expect(at(9, 5, 3)).toBe("09:05:03");
    expect(at(23, 59, 59)).toBe("23:59:59");
    expect(at(0, 0, 0)).toBe("00:00:00");
  });

  it("shows seconds, because that is the proof of life", () => {
    const base = new Date(2026, 8, 11, 12, 0, 0).getTime();
    expect(formatClock(base)).not.toBe(formatClock(base + 1000));
  });
});
