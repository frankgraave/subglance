import { describe, expect, it } from "vitest";
import { floorPercent, formatUptime } from "./format";

describe("formatUptime", () => {
  it("keeps 21 down checks out of 43,138 off 100%", () => {
    // The demo's "Marketing site": 30d read "100%" directly above
    // "21 of 43138 confirmed down". The headline contradicted its own count.
    const pct = ((43_138 - 21) / 43_138) * 100;
    expect(formatUptime(pct)).toBe("99.9%");
  });

  it("writes 100% only for a window with no confirmed-down check", () => {
    expect(formatUptime(100)).toBe("100%");
    // The closest a real count can get without being perfect: still not 100.
    expect(formatUptime(((1_000_000 - 1) / 1_000_000) * 100)).toBe("99.9%");
    expect(formatUptime(99.95)).toBe("99.9%");
    expect(formatUptime(99.99)).toBe("99.9%");
  });

  it("rounds down everywhere in between, so no digit claims more than happened", () => {
    expect(formatUptime(97.29)).toBe("97.2%");
    expect(formatUptime(50)).toBe("50.0%");
    expect(formatUptime(0.04)).toBe("0.0%");
  });

  it("does not let float noise take a digit off an exact share", () => {
    // The API computes up / total * 100, and in binary floating point 29 of
    // 100 comes out as 28.999999999999996. A bare floor would print 28.9.
    expect(formatUptime((29 / 100) * 100)).toBe("29.0%");
    expect(formatUptime((23 / 40) * 100)).toBe("57.5%");
    expect(floorPercent((29 / 50) * 100, 2)).toBe("58.00");
  });

  it("writes the two exact ends as whole numbers", () => {
    expect(formatUptime(0)).toBe("0%");
    expect(formatUptime(100)).toBe("100%");
  });
});

describe("floorPercent", () => {
  it("never rounds a near-perfect share up to 100", () => {
    expect(floorPercent(99.995, 2)).toBe("99.99");
    expect(floorPercent(99.9999999999, 2)).toBe("99.99");
    // Close enough that the float-noise epsilon alone would carry it over.
    expect(floorPercent(100 - 1e-12, 2)).toBe("99.99");
    expect(floorPercent(100, 2)).toBe("100.00");
  });

  it("truncates rather than rounding to nearest", () => {
    expect(floorPercent((2 / 3) * 100, 2)).toBe("66.66");
    expect(floorPercent(90, 2)).toBe("90.00");
  });
});
