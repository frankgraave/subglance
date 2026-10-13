import { describe, expect, it } from "vitest";
import { backLabel, originFrom, readOrigin } from "./listOrigin";

describe("originFrom", () => {
  it("records the dashboard and the inventory as lists to return to", () => {
    expect(originFrom({ name: "dashboard" }, 3, null)).toEqual({ list: "dashboard", index: 3 });
    expect(originFrom({ name: "monitors", create: false }, 4, null)).toEqual({ list: "monitors", index: 4 });
  });

  it("records nothing for the add form, which returning would reopen", () => {
    expect(originFrom({ name: "monitors", create: true }, 4, null)).toBeNull();
  });

  it("records nothing for a screen that is not a list", () => {
    for (const from of [{ name: "incidents" }, { name: "settings" }, { name: "notFound", path: "/x" }] as const) {
      expect(originFrom(from, 2, { list: "dashboard", index: 0 })).toBeNull();
    }
  });

  it("keeps the first page's origin when one monitor opens another", () => {
    const first = { list: "monitors", index: 1 } as const;
    expect(originFrom({ name: "monitor", id: "1" }, 2, first)).toBe(first);
    expect(originFrom({ name: "monitor", id: "1" }, 2, null)).toBeNull();
  });
});

describe("readOrigin", () => {
  it("reads back what was written", () => {
    expect(readOrigin({ list: "dashboard", index: 0 })).toEqual({ list: "dashboard", index: 0 });
  });

  it("reads anything else as no origin, since history state outlives a reload", () => {
    for (const value of [
      undefined,
      null,
      "dashboard",
      { list: "settings", index: 1 },
      { list: "monitors" },
      { list: "monitors", index: -1 },
      { list: "monitors", index: 1.5 },
      { list: "monitors", index: "1" },
    ]) {
      expect(readOrigin(value)).toBeNull();
    }
  });
});

describe("backLabel", () => {
  it("names the place the control goes", () => {
    expect(backLabel({ list: "dashboard", index: 0 })).toBe("Dashboard");
    expect(backLabel({ list: "monitors", index: 0 })).toBe("All monitors");
    expect(backLabel(null)).toBe("All monitors");
  });
});
