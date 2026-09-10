/**
 * Theme resolution.
 *
 * Three user-visible choices — light, dark, or follow the operating system —
 * resolved down to the two themes the tokens actually define. "system" is the
 * default because a monitoring dashboard is often left open all day; forcing
 * dark on someone whose machine is in light mode makes it the odd window out.
 */

export type ThemePreference = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

/** Where the preference is persisted. Read by the pre-paint script in index.html. */
export const THEME_STORAGE_KEY = "subglance:theme";

const PREFERENCES: readonly ThemePreference[] = ["light", "dark", "system"];

/** Narrows unknown input (localStorage, an API response) to a valid preference. */
export function isThemePreference(value: unknown): value is ThemePreference {
  return typeof value === "string" && (PREFERENCES as readonly string[]).includes(value);
}

/**
 * Resolves a preference against the OS setting.
 *
 * `systemPrefersDark` is passed in rather than read here so this stays a pure
 * function: it is the part worth testing, and it must not need a browser.
 */
export function resolveTheme(
  preference: ThemePreference,
  systemPrefersDark: boolean,
): ResolvedTheme {
  if (preference === "system") {
    return systemPrefersDark ? "dark" : "light";
  }
  return preference;
}

/** Reads the stored preference, falling back to "system" for absent or corrupt values. */
export function readStoredPreference(storage: Pick<Storage, "getItem">): ThemePreference {
  let raw: string | null;
  try {
    raw = storage.getItem(THEME_STORAGE_KEY);
  } catch {
    // Safari in private mode throws on storage access. A theme is not worth
    // taking the dashboard down for.
    return "system";
  }
  return isThemePreference(raw) ? raw : "system";
}

/**
 * Applies a resolved theme to the document element.
 *
 * The tokens hang off `[data-theme]`, so this attribute is the switch. It is
 * set on <html> and not <body> so the page background is right before the
 * body element exists.
 */
export function applyTheme(root: Element, theme: ResolvedTheme): void {
  root.setAttribute("data-theme", theme);
}
