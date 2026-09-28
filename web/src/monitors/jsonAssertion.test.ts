import { describe, expect, it } from "vitest";
import { assertionFrom, expectedProblem, expectedText, readExpected } from "./jsonAssertion";

describe("readExpected", () => {
  it("reads a bare word as a string, so the common case needs no quotes", () => {
    expect(readExpected("up")).toBe("up");
    expect(readExpected("two words")).toBe("two words");
  });

  it("keeps the JSON type of numbers, booleans and null", () => {
    expect(readExpected("1")).toBe(1);
    expect(readExpected("1.5")).toBe(1.5);
    expect(readExpected("true")).toBe(true);
    expect(readExpected("false")).toBe(false);
    expect(readExpected("null")).toBeNull();
  });

  it("reads a quoted number as the string it is", () => {
    expect(readExpected('"1"')).toBe("1");
    expect(readExpected('"true"')).toBe("true");
  });

  it("does not turn braces into an object the API would refuse", () => {
    expect(readExpected('{"a":1}')).toBe('{"a":1}');
    expect(readExpected("[1]")).toBe("[1]");
  });
});

describe("expectedText", () => {
  it("writes each value so that reading it back gives the same value", () => {
    for (const value of ["up", "1", "true", "null", 1, 0.5, true, false, null, "two words"]) {
      expect(readExpected(expectedText(value))).toEqual(value);
    }
  });

  it("leaves a plain string unquoted", () => {
    expect(expectedText("up")).toBe("up");
    expect(expectedText("1")).toBe('"1"');
    expect(expectedText(undefined)).toBe("");
  });
});

describe("assertionFrom", () => {
  it("is null without a path", () => {
    expect(assertionFrom("  ", "equals", "up")).toBeNull();
  });

  it("sends no expected value for exists", () => {
    expect(assertionFrom("items[0]", "exists", "ignored")).toEqual({ path: "items[0]", operator: "exists" });
  });

  it("types the expected value", () => {
    expect(assertionFrom(" checks.db.status ", "equals", "up")).toEqual({
      path: "checks.db.status", operator: "equals", expected: "up",
    });
    expect(assertionFrom("lag", "less_than", "30")).toEqual({ path: "lag", operator: "less_than", expected: 30 });
  });
});

describe("expectedProblem", () => {
  it("accepts every number a double holds exactly, however it is written", () => {
    for (const text of ["1", "-1", "0", "-0", "1.5", "1.50", "0.1", "1e3", "1E-3", "2.5e+2", "100", "9007199254740991", "up", '"0.1234567890123456789"', "true", ""]) {
      expect(expectedProblem(text)).toBeNull();
    }
  });

  it("refuses a number that would reach the API rounded", () => {
    for (const text of ["0.1234567890123456789", "9007199254740993", " 12345678901234567890 "]) {
      expect(expectedProblem(text)).toMatch(/more digits than a browser can hold/);
    }
    expect(expectedProblem("9007199254740993")).toMatch(/sent as 9007199254740992/);
    expect(expectedProblem("1e400")).toMatch(/sent as Infinity/);
  });
});
