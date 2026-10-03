import { describe, expect, it } from "vitest";
import {
  DAYS_ONLY, MINUTES_TO_HOURS, SECONDS_TO_DAYS, SECONDS_TO_HOURS,
  DURATION_LIMITS, amountIn, durationAllowed, sameWire, toWire, unitFor,
} from "./duration";

describe("unitFor", () => {
  it.each([
    ["900", "min", "15"],
    ["3600", "h", "1"],
    ["5400", "min", "90"],
    ["731", "s", "731"],
    ["86400", "h", "24"],
    ["60", "min", "1"],
  ])("shows %s seconds in the largest unit that holds it exactly", (wire, unit, amount) => {
    const chosen = unitFor(wire, SECONDS_TO_HOURS);
    expect(chosen.id).toBe(unit);
    expect(amountIn(wire, chosen)).toBe(amount);
  });

  it("reaches days only where the field offers them", () => {
    expect(unitFor("86400", SECONDS_TO_DAYS).id).toBe("d");
    expect(unitFor("172800", SECONDS_TO_HOURS).id).toBe("h");
  });

  it.each(["", "0", "-60", "abc"])("falls back to the smallest unit for %j", (wire) => {
    expect(unitFor(wire, SECONDS_TO_HOURS).id).toBe("s");
  });

  it("counts a wire value that is already minutes in minutes or hours", () => {
    expect(unitFor("90", MINUTES_TO_HOURS).id).toBe("min");
    expect(unitFor("120", MINUTES_TO_HOURS).id).toBe("h");
    expect(amountIn("120", unitFor("120", MINUTES_TO_HOURS))).toBe("2");
  });
});

describe("toWire", () => {
  const [seconds, minutes, hours] = SECONDS_TO_HOURS;
  it("multiplies an amount out to the wire unit", () => {
    expect(toWire("15", minutes)).toBe("900");
    expect(toWire("1.5", hours)).toBe("5400");
    expect(toWire("731", seconds)).toBe("731");
    expect(toWire("30", DAYS_ONLY[0])).toBe("30");
  });

  it("absorbs binary fractions without rounding to a whole number", () => {
    // 1.1 * 60 is 66.00000000000001 in floating point.
    expect(toWire("1.1", minutes)).toBe("66");
    // A fraction of a second must survive, so the form's check refuses it.
    expect(toWire("1.01", minutes)).toBe("60.6");
  });

  it.each(["", " ", "abc"])("passes %j through for the form's check to refuse", (amount) => {
    expect(toWire(amount, minutes)).toBe(amount);
  });
});

describe("sameWire", () => {
  it("treats a number and its text as one value", () => {
    // A form holding numbers hands an emptied box back as 0; that is not a
    // new value from outside, and must not overwrite the empty box.
    expect(sameWire("0", "")).toBe(true);
    expect(sameWire("900", "900")).toBe(true);
    expect(sameWire("NaN", "abc")).toBe(true);
    expect(sameWire("60", "61")).toBe(false);
  });
});

describe("durationAllowed", () => {
  it.each([
    ["interval_s", 20, true], ["interval_s", 19, false], ["interval_s", 86400, true], ["interval_s", 86401, false],
    ["interval_s", 60.6, false], ["timeout_s", 120, true], ["timeout_s", 0, false],
    ["push_interval_s", 59, false], ["push_grace_s", 0, true], ["ssl_warn_days", 366, false],
  ] as const)("%s = %s is allowed: %s", (field, value, allowed) => {
    expect(durationAllowed(field, value)).toBe(allowed);
  });

  it("states each range the way the product writes a length of time", () => {
    expect(DURATION_LIMITS.interval_s.message).toBe("The check interval must be between 20 s and 1 d, in whole seconds.");
    expect(DURATION_LIMITS.push_grace_s.message).toBe("The allowed lateness must be between 0 s and 30 d, in whole seconds.");
    expect(DURATION_LIMITS.ssl_warn_days.message).toBe("The certificate warning must be between 1 d and 365 d, in whole days.");
  });

  it("refuses an empty value rather than reading it as zero", () => {
    expect(durationAllowed("push_grace_s", "")).toBe(false);
  });
});
