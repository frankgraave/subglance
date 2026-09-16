import { describe, expect, it } from "vitest";
import { describeAge, describeGap } from "./age";

const now = Date.parse("2026-09-11T12:00:00Z");
const ago = (ms: number) => now - ms;

describe("describeAge", () => {
  it("says 'just now' inside the first minute", () => {
    expect(describeAge(ago(0), now)).toBe("just now");
    expect(describeAge(ago(59_000), now)).toBe("just now");
  });

  it("counts whole minutes, then whole hours", () => {
    expect(describeAge(ago(60_000), now)).toBe("1 min ago");
    expect(describeAge(ago(59 * 60_000), now)).toBe("59 min ago");
    expect(describeAge(ago(60 * 60_000), now)).toBe("1 h ago");
    expect(describeAge(ago(150 * 60_000), now)).toBe("2 h ago");
  });

  it("has nothing to say without a timestamp", () => {
    // A monitor that was never checked has no age; "0 min ago" would claim it
    // was just measured.
    expect(describeAge(null, now)).toBeNull();
    expect(describeAge(undefined, now)).toBeNull();
  });

  it("refuses a future timestamp rather than printing a negative age", () => {
    expect(describeAge(now + 5_000, now)).toBeNull();
  });
});

describe("describeGap", () => {
  /*
   * The same arithmetic, worded as a silence rather than as a moment
   * (SUB-111). "checked 4 min ago" dates a reading and stays true forever;
   * "no data for 4 min" describes a gap that is still growing, which is the
   * sentence a dead stream needs.
   */

  it("names the span without the 'ago', so it reads as an ongoing silence", () => {
    expect(describeGap(ago(60_000), now)).toBe("1 min");
    expect(describeGap(ago(59 * 60_000), now)).toBe("59 min");
    expect(describeGap(ago(60 * 60_000), now)).toBe("1 h");
    expect(describeGap(ago(150 * 60_000), now)).toBe("2 h");
  });

  it("spells out a sub-minute gap rather than rounding it to zero", () => {
    // "no data for 0 min" reads as a rendering fault, and "no data for just
    // now" is not a sentence — so the first minute gets words of its own.
    expect(describeGap(ago(0), now)).toBe("under a minute");
    expect(describeGap(ago(59_000), now)).toBe("under a minute");
  });

  it("has nothing to say without a timestamp, or about the future", () => {
    expect(describeGap(null, now)).toBeNull();
    expect(describeGap(undefined, now)).toBeNull();
    expect(describeGap(now + 5_000, now)).toBeNull();
  });
});
