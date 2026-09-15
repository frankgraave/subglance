import { describe, expect, it } from "vitest";
import {
  CARD_COLUMNS_STORAGE_KEY,
  DEFAULT_CARD_COLUMNS,
  DEFAULT_LAYOUT,
  LAYOUT_STORAGE_KEY,
  SIDEBAR_STORAGE_KEY,
  effectiveLayout,
  isCardColumns,
  isLayoutId,
  readStoredCardColumns,
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

describe("cards per row", () => {
  it("accepts only the four known counts", () => {
    for (const id of ["1", "2", "3", "auto"]) expect(isCardColumns(id)).toBe(true);
    // The shapes a hand-edited or older value could plausibly take: a number
    // rather than a string, a count nobody offers, the empty string.
    expect(isCardColumns(2)).toBe(false);
    expect(isCardColumns("4")).toBe(false);
    expect(isCardColumns("")).toBe(false);
    expect(isCardColumns(null)).toBe(false);
  });

  it("reads a stored count back", () => {
    const storage = fakeStorage({ [CARD_COLUMNS_STORAGE_KEY]: "3" });
    expect(readStoredCardColumns(storage)).toBe("3");
  });

  it("falls back to the default for an absent or corrupt value", () => {
    expect(readStoredCardColumns(fakeStorage())).toBe(DEFAULT_CARD_COLUMNS);
    expect(
      readStoredCardColumns(fakeStorage({ [CARD_COLUMNS_STORAGE_KEY]: "17" })),
    ).toBe(DEFAULT_CARD_COLUMNS);
  });

  it("survives a storage that throws", () => {
    expect(readStoredCardColumns(hostile)).toBe(DEFAULT_CARD_COLUMNS);
  });

  it("keeps its own key, so a layout change cannot reset it", () => {
    /*
     * The reason this is not folded into the layout id: the setting has to
     * survive a trip through Rows and back. Storing "cards-2" would mean
     * switching layouts twice silently discards a choice the user made once.
     */
    expect(CARD_COLUMNS_STORAGE_KEY).not.toBe(LAYOUT_STORAGE_KEY);
    const storage = fakeStorage({ [CARD_COLUMNS_STORAGE_KEY]: "3" });
    writePreference(storage, LAYOUT_STORAGE_KEY, "rows");
    writePreference(storage, LAYOUT_STORAGE_KEY, "cards");
    expect(readStoredCardColumns(storage)).toBe("3");
  });
});
