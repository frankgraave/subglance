import { useCallback, useEffect, useState } from "react";
import {
  applyTheme,
  readStoredPreference,
  resolveTheme,
  THEME_STORAGE_KEY,
  type ResolvedTheme,
  type ThemePreference,
} from "./theme";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/**
 * Owns the theme preference: persists it, keeps `[data-theme]` in sync, and
 * follows the OS while the preference is "system".
 *
 * Deliberately not a context. Exactly one component needs to write the theme;
 * everything else reads it through CSS. A provider would be indirection for a
 * single consumer.
 */
export function useTheme(): {
  preference: ThemePreference;
  resolved: ResolvedTheme;
  setPreference: (next: ThemePreference) => void;
} {
  const [preference, setPreferenceState] = useState<ThemePreference>(() =>
    readStoredPreference(window.localStorage),
  );
  const [systemPrefersDark, setSystemPrefersDark] = useState(
    () => window.matchMedia(DARK_QUERY).matches,
  );

  // Track the OS setting even when the preference is explicit: the user can
  // switch back to "system" at any time and must not see a stale value.
  useEffect(() => {
    const query = window.matchMedia(DARK_QUERY);
    const onChange = (event: MediaQueryListEvent) => setSystemPrefersDark(event.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);

  const resolved = resolveTheme(preference, systemPrefersDark);

  useEffect(() => {
    applyTheme(document.documentElement, resolved);
  }, [resolved]);

  const setPreference = useCallback((next: ThemePreference) => {
    setPreferenceState(next);
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // Non-fatal: the theme still applies for this session.
    }
  }, []);

  return { preference, resolved, setPreference };
}
