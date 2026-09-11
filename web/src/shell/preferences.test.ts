import { describe, expect, it } from "vitest";
import {
  DEFAULT_LAYOUT,
  LAYOUT_STORAGE_KEY,
  SIDEBAR_STORAGE_KEY,
  effectiveLayout,
  isLayoutId,
  readStoredLayout,
  readStoredSidebarCollapsed,
  writePreference,
} from "./preferences";

/** A storage that throws, like Safari in private mode. */
const hostile: Storage = {
  getItem() {
    throw new Error("SecurityError");
  },
  setItem() {
    throw new Error("SecurityError");
  },
  removeItem() {},
  clear() {},
  key: () => null,
  length: 0,
};

function fakeStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
    clear: () => data.clear(),
    key: (i) => [...data.keys()][i] ?? null,
    get length() {
      return data.size;
    },
  } as Storage;
}

describe("layout preference", () => {
  it("accepts only the four known layouts", () => {
    expect(isLayoutId("rows")).toBe(true);
    expect(isLayoutId("wall")).toBe(true);
    expect(isLayoutId("grid")).toBe(false);
    expect(isLayoutId(null)).toBe(false);
  });

  it("reads a stored layout back", () => {
    expect(readStoredLayout(fakeStorage({ [LAYOUT_STORAGE_KEY]: "compact" }))).toBe("compact");
  });

  it("falls back to the default for an absent or corrupt value", () => {
    expect(readStoredLayout(fakeStorage())).toBe(DEFAULT_LAYOUT);
    expect(readStoredLayout(fakeStorage({ [LAYOUT_STORAGE_KEY]: "spreadsheet" }))).toBe(
      DEFAULT_LAYOUT,
    );
  });

  it("survives a storage that throws", () => {
    expect(readStoredLayout(hostile)).toBe(DEFAULT_LAYOUT);
    expect(readStoredSidebarCollapsed(hostile)).toBe(false);
    expect(() => writePreference(hostile, LAYOUT_STORAGE_KEY, "cards")).not.toThrow();
  });
});

describe("sidebar preference", () => {
  it("collapses only on the exact stored value", () => {
    expect(readStoredSidebarCollapsed(fakeStorage({ [SIDEBAR_STORAGE_KEY]: "collapsed" }))).toBe(
      true,
    );
    expect(readStoredSidebarCollapsed(fakeStorage({ [SIDEBAR_STORAGE_KEY]: "expanded" }))).toBe(
      false,
    );
    // Fails open: an unrecognised value must not hide the navigation.
    expect(readStoredSidebarCollapsed(fakeStorage({ [SIDEBAR_STORAGE_KEY]: "yes" }))).toBe(false);
    expect(readStoredSidebarCollapsed(fakeStorage())).toBe(false);
  });
});

describe("the viewport's veto", () => {
  it("honours every preference on a wide screen", () => {
    for (const id of ["rows", "cards", "compact", "wall"] as const) {
      expect(effectiveLayout(id, false)).toBe(id);
    }
  });

  it("downgrades the two column layouts to cards when narrow", () => {
    expect(effectiveLayout("rows", true)).toBe("cards");
    expect(effectiveLayout("compact", true)).toBe("cards");
  });

  it("leaves cards and the wall alone when narrow", () => {
    expect(effectiveLayout("cards", true)).toBe("cards");
    // The wall is lamps and names; it fits a phone as happily as a TV, and
    // taking someone out of it because they rotated the device would be rude.
    expect(effectiveLayout("wall", true)).toBe("wall");
  });
});
