import { describe, expect, it } from "vitest";
import {
  formatClock,
  formatCount,
  formatDate,
  formatDateIso,
  formatDay,
  formatDayTime,
  formatDuration,
  formatMoment,
  formatMomentFrom,
  formatMomentIso,
  formatRunTime,
  formatUptime,
  sameDay,
} from "./format";

/*
 * Local times on purpose: every formatter writes the reader's own zone, so a
 * fixture built with the local Date constructor reads the same on any
 * machine the suite runs on, in any TZ.
 */
const at = (y: number, mo: number, d: number, h = 0, mi = 0, s = 0) => new Date(y, mo, d, h, mi, s).getTime();

describe("dates and times", () => {
  const t = at(2026, 9, 1, 13, 59, 44);

  it("writes each kind in its one shape", () => {
    expect(formatMoment(t)).toBe("1 Oct 2026, 13:59");
    expect(formatDate(t)).toBe("1 Oct 2026");
    expect(formatDay(t)).toBe("1 Oct");
    expect(formatDayTime(t)).toBe("1 Oct, 13:59");
    expect(formatDayTime(t, true)).toBe("1 Oct, 13:59:44");
    expect(formatClock(t)).toBe("13:59");
    expect(formatClock(t, true)).toBe("13:59:44");
  });

  it("uses a 24-hour clock with no meridiem, padded so a column never jumps", () => {
    expect(formatClock(at(2026, 9, 1, 0, 5))).toBe("00:05");
    expect(formatClock(at(2026, 9, 1, 9, 5, 3), true)).toBe("09:05:03");
    expect(formatMoment(at(2026, 9, 1, 23, 0))).toBe("1 Oct 2026, 23:00");
    expect(formatMoment(t)).not.toMatch(/AM|PM|am|pm/);
  });

  it("names the month in English three letters, every month", () => {
    const months = Array.from({ length: 12 }, (_, m) => formatDay(at(2026, m, 9)));
    expect(months).toEqual([
      "9 Jan", "9 Feb", "9 Mar", "9 Apr", "9 May", "9 Jun",
      "9 Jul", "9 Aug", "9 Sep", "9 Oct", "9 Nov", "9 Dec",
    ]);
  });

  it("reads an ISO string from the API in the reader's zone", () => {
    const iso = new Date(t).toISOString();
    expect(formatMomentIso(iso)).toBe("1 Oct 2026, 13:59");
    expect(formatDateIso(iso)).toBe("1 Oct 2026");
  });

  it("never prints Invalid Date", () => {
    expect(formatMoment(null)).toBeNull();
    expect(formatMoment(undefined)).toBeNull();
    expect(formatMoment(Number.NaN)).toBeNull();
    expect(formatClock(null)).toBeNull();
    expect(formatMomentIso("not a date")).toBe("—");
    expect(formatDateIso("not a date")).toBe("—");
    expect(formatDay(Number.NaN)).toBe("—");
    expect(formatDayTime(Number.NaN)).toBe("—");
    expect(formatClock(Number.NaN)).toBe("—");
  });
});

describe("formatMomentFrom", () => {
  it("drops the date an earlier part of the sentence already gave", () => {
    expect(formatMomentFrom(at(2026, 9, 1, 14, 15), at(2026, 9, 1, 9, 0))).toBe("14:15");
  });

  it("keeps the date when the day changed in between", () => {
    expect(formatMomentFrom(at(2026, 9, 2, 2, 40), at(2026, 9, 1, 23, 0))).toBe("2 Oct 2026, 02:40");
    expect(formatMomentFrom(at(2026, 9, 1, 14, 15), at(2025, 9, 1, 14, 0))).toBe("1 Oct 2026, 14:15");
  });

  it("keeps the date when there is nothing to share it with", () => {
    expect(formatMomentFrom(at(2026, 9, 1, 14, 15), null)).toBe("1 Oct 2026, 14:15");
    expect(formatMomentFrom(null, at(2026, 9, 1))).toBeNull();
  });

  it("compares calendar days, not 24-hour spans", () => {
    expect(sameDay(at(2026, 9, 1, 0, 0), at(2026, 9, 1, 23, 59))).toBe(true);
    expect(sameDay(at(2026, 9, 1, 23, 59), at(2026, 9, 2, 0, 1))).toBe(false);
  });
});

describe("formatDuration", () => {
  it("keeps seconds only below a minute, where they are the whole story", () => {
    expect(formatDuration(0)).toBe("0 s");
    expect(formatDuration(59)).toBe("59 s");
  });

  it("drops seconds above a minute rather than reading out three units", () => {
    expect(formatDuration(60)).toBe("1 min");
    expect(formatDuration(3599)).toBe("59 min");
  });

  it("gives hours a minute remainder, and drops it when it is zero", () => {
    expect(formatDuration(3600)).toBe("1 h");
    expect(formatDuration(3600 + 41 * 60 + 12)).toBe("1 h 41 min");
  });

  it("rolls over to days", () => {
    expect(formatDuration(86400)).toBe("1 d");
    expect(formatDuration(86400 + 7200)).toBe("1 d 2 h");
  });

  it("says unknown rather than printing a negative or NaN duration", () => {
    expect(formatDuration(-5)).toBe("unknown");
    expect(formatDuration(Number.NaN)).toBe("unknown");
  });
});


describe("formatRunTime", () => {
  it("keeps a tenth of a second below ten seconds", () => {
    expect(formatRunTime(400)).toBe("0.4 s");
    expect(formatRunTime(9_940)).toBe("9.9 s");
  });

  it("is a duration above that, in the same units as every other", () => {
    expect(formatRunTime(12_000)).toBe("12 s");
    expect(formatRunTime(185_000)).toBe("3 min");
    expect(formatRunTime(3_660_000)).toBe("1 h 1 min");
  });
});

describe("formatCount", () => {
  it("groups thousands with a comma, whatever the browser's locale", () => {
    expect(formatCount(43_138)).toBe("43,138");
    expect(formatCount(1_000_000)).toBe("1,000,000");
    expect(formatCount(7)).toBe("7");
  });
});

describe("formatUptime", () => {
  it("writes two decimals on every figure, the exact ends included", () => {
    expect(formatUptime(100)).toBe("100.00%");
    expect(formatUptime(0)).toBe("0.00%");
    expect(formatUptime(50)).toBe("50.00%");
    expect(formatUptime(97.29)).toBe("97.29%");
  });

  it("keeps 21 down checks out of 43,138 off 100%", () => {
    // The demo's "Marketing site": 30d read "100%" directly above
    // "21 of 43138 confirmed down". The headline contradicted its own count.
    expect(formatUptime(((43_138 - 21) / 43_138) * 100)).toBe("99.95%");
  });

  it("writes 100.00% only for a window with no confirmed-down check", () => {
    // The closest a real count can get without being perfect: still not 100.
    expect(formatUptime(((1_000_000 - 1) / 1_000_000) * 100)).toBe("99.99%");
    expect(formatUptime(99.995)).toBe("99.99%");
    expect(formatUptime(99.9999999999)).toBe("99.99%");
    // Close enough that the float-noise epsilon alone would carry it over.
    expect(formatUptime(100 - 1e-12)).toBe("99.99%");
  });

  it("rounds down, never to nearest", () => {
    expect(formatUptime((2 / 3) * 100)).toBe("66.66%");
    expect(formatUptime(0.004)).toBe("0.00%");
  });

  it("does not let float noise take a digit off an exact share", () => {
    // The API computes up / total * 100, and in binary floating point 29 of
    // 100 comes out as 28.999999999999996. A bare floor would print 28.99.
    expect(formatUptime((29 / 100) * 100)).toBe("29.00%");
    expect(formatUptime((23 / 40) * 100)).toBe("57.50%");
    expect(formatUptime((29 / 50) * 100)).toBe("58.00%");
  });
});
