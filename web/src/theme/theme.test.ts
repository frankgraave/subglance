import { describe, expect, it } from "vitest";
import {
  applyTheme,
  isThemePreference,
  readStoredPreference,
  resolveTheme,
  THEME_STORAGE_KEY,
} from "./theme";

describe("resolveTheme", () => {
  it("returns an explicit preference regardless of the system setting", () => {
    expect(resolveTheme("light", true)).toBe("light");
    expect(resolveTheme("dark", false)).toBe("dark");
  });

  it("follows the system setting when the preference is 'system'", () => {
    expect(resolveTheme("system", true)).toBe("dark");
    expect(resolveTheme("system", false)).toBe("light");
  });
});

describe("isThemePreference", () => {
  it("accepts the three valid values and rejects everything else", () => {
    expect(isThemePreference("light")).toBe(true);
    expect(isThemePreference("dark")).toBe(true);
    expect(isThemePreference("system")).toBe(true);
    expect(isThemePreference("Dark")).toBe(false);
    expect(isThemePreference("")).toBe(false);
    expect(isThemePreference(null)).toBe(false);
    expect(isThemePreference(2)).toBe(false);
  });
});

describe("readStoredPreference", () => {
  it("returns the stored value when it is valid", () => {
    const storage = { getItem: () => "light" };
    expect(readStoredPreference(storage)).toBe("light");
  });

  it("falls back to 'system' for absent or corrupt values", () => {
    expect(readStoredPreference({ getItem: () => null })).toBe("system");
    expect(readStoredPreference({ getItem: () => "purple" })).toBe("system");
  });

  it("falls back to 'system' when storage access throws", () => {
    // Safari in private mode does this. The dashboard must still render.
    const storage = {
      getItem: () => {
        throw new Error("SecurityError");
      },
    };
    expect(readStoredPreference(storage)).toBe("system");
  });

  it("reads from the key the pre-paint script in index.html uses", () => {
    let asked = "";
    readStoredPreference({
      getItem: (key: string) => {
        asked = key;
        return null;
      },
    });
    expect(asked).toBe(THEME_STORAGE_KEY);
  });
});

describe("applyTheme", () => {
  it("sets data-theme, which is what the token blocks key off", () => {
    const attributes = new Map<string, string>();
    const root = {
      setAttribute: (name: string, value: string) => void attributes.set(name, value),
    } as unknown as Element;

    applyTheme(root, "light");
    expect(attributes.get("data-theme")).toBe("light");
    applyTheme(root, "dark");
    expect(attributes.get("data-theme")).toBe("dark");
  });
});
