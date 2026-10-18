import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { localInputValue, localInstant, oneOffSpan, weeklySpan, windowState } from "./maintenanceTimes";

/*
 * Pinned to Amsterdam, which is UTC+2 in summer and UTC+1 in winter, so the
 * conversion is visible and the two daylight-saving days of 2026 fall inside
 * the zone: 29 March skips 02:00-03:00, 25 October repeats it. Node reads TZ
 * whenever a Date is made, so setting it here holds for this file alone.
 */
let zone: string | undefined;
beforeAll(() => { zone = process.env.TZ; process.env.TZ = "Europe/Amsterdam"; });
afterAll(() => { if (zone === undefined) delete process.env.TZ; else process.env.TZ = zone; });

describe("a one-off window is typed on the reader's clock", () => {
  it("sends 20:00 in Amsterdam as 18:00 UTC in summer time", () => {
    expect(localInstant("2026-10-20T20:00")).toEqual({ ok: true, iso: "2026-10-20T18:00:00.000Z" });
    expect(localInstant("2026-10-20T21:00")).toEqual({ ok: true, iso: "2026-10-20T19:00:00.000Z" });
  });

  it("sends 20:00 as 19:00 UTC once the clocks have gone back", () => {
    expect(localInstant("2026-11-03T20:00")).toEqual({ ok: true, iso: "2026-11-03T19:00:00.000Z" });
  });

  it("takes the earlier of a time that happens twice when the clocks go back", () => {
    // 02:30 on 25 October 2026 is 00:30 UTC (summer time) and 01:30 UTC (winter time).
    expect(localInstant("2026-10-25T02:30")).toEqual({ ok: true, iso: "2026-10-25T00:30:00.000Z" });
  });

  it("refuses a time the clocks skip rather than moving it", () => {
    const refused = localInstant("2026-03-29T02:30");
    expect(refused.ok).toBe(false);
    expect(refused.ok ? "" : refused.message).toContain("02:30 does not happen on that day");
    expect(localInstant("2026-03-29T03:00")).toEqual({ ok: true, iso: "2026-03-29T01:00:00.000Z" });
  });

  it("leaves a value that is no date and time for the server to refuse", () => {
    expect(localInstant("")).toEqual({ ok: true, iso: "" });
    expect(localInstant("2026-10-20")).toEqual({ ok: true, iso: "2026-10-20" });
  });

  it("refuses a date that does not exist rather than rolling it over", () => {
    expect(localInstant("2026-02-30T20:00")).toEqual({ ok: false, message: "That date does not exist." });
  });

  it("writes an instant back as the field holds it", () => {
    expect(localInputValue(Date.parse("2026-10-20T18:00:00Z"))).toBe("2026-10-20T20:00");
    expect(localInputValue(Number.NaN)).toBe("");
  });
});

describe("the list reads a window in the reader's zone", () => {
  it("dates the end only when it falls on another day", () => {
    expect(oneOffSpan("2026-10-01T18:00:00Z", "2026-10-01T19:00:00Z")).toBe("1 Oct 2026, 20:00 → 21:00");
    expect(oneOffSpan("2026-10-01T21:00:00Z", "2026-10-01T23:00:00Z")).toBe("1 Oct 2026, 23:00 → 2 Oct 2026, 01:00");
  });

  it("writes no ISO timestamp", () => {
    expect(oneOffSpan("2026-10-01T18:00:00Z", "2026-10-03T19:00:00Z")).not.toMatch(/T\d\d:\d\d|Z\b/);
  });

  it("keeps a weekly window on its own zone's clock and says which", () => {
    expect(weeklySpan([0, 3], "02:00", "UTC", 90)).toBe("Sunday, Wednesday at 02:00 (UTC), for 1 h 30 min");
  });
});

describe("where a window stands", () => {
  const now = Date.parse("2026-10-20T12:00:00Z");
  const oneOff = (starts: string, ends: string, active = false) => ({ starts_at: starts, ends_at: ends, active });

  it("is active when the server says so", () => {
    expect(windowState(oneOff("2026-10-20T11:00:00Z", "2026-10-20T13:00:00Z", true), now)).toBe("active");
  });

  it("is upcoming before its start and ended after its end", () => {
    expect(windowState(oneOff("2026-10-21T11:00:00Z", "2026-10-21T13:00:00Z"), now)).toBe("upcoming");
    expect(windowState(oneOff("2026-10-19T11:00:00Z", "2026-10-19T13:00:00Z"), now)).toBe("ended");
    // The end is excluded from the window, so at the end it has ended.
    expect(windowState(oneOff("2026-10-20T11:00:00Z", "2026-10-20T12:00:00Z"), now)).toBe("ended");
  });

  it("never ends when weekly", () => {
    expect(windowState({ timezone: "UTC", active: false }, now)).toBe("upcoming");
  });
});
