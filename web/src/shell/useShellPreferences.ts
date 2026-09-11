import { useCallback, useState } from "react";
import {
  LAYOUT_STORAGE_KEY,
  SIDEBAR_STORAGE_KEY,
  readStoredLayout,
  readStoredSidebarCollapsed,
  writePreference,
  type LayoutId,
} from "./preferences";

/**
 * The two shell preferences as state, persisted on every change.
 *
 * Lazy initialisers rather than an effect: the first render is already the
 * stored value, so nobody watches the sidebar pop open and collapse again
 * after paint. Writing inside the setter (not in an effect keyed on the value)
 * keeps the write tied to the user's action instead of to a render.
 *
 * There is no provider. Exactly one component owns these, and everything below
 * receives them as props — the same reasoning `useTheme` documents.
 */

export type ShellPreferences = {
  layout: LayoutId;
  setLayout: (next: LayoutId) => void;
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;
};

/** Injected in tests; window.localStorage in the browser. */
export type PreferenceStorage = Pick<Storage, "getItem" | "setItem">;

export function useShellPreferences(storage: PreferenceStorage): ShellPreferences {
  const [layout, setLayoutState] = useState<LayoutId>(() => readStoredLayout(storage));
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() =>
    readStoredSidebarCollapsed(storage),
  );

  const setLayout = useCallback(
    (next: LayoutId) => {
      setLayoutState(next);
      writePreference(storage, LAYOUT_STORAGE_KEY, next);
    },
    [storage],
  );

  // Status wall hides the sidebar without touching this preference, so
  // leaving the wall gives the rail back exactly as it was (DESIGN.md §7).
  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((collapsed) => {
      const next = !collapsed;
      writePreference(storage, SIDEBAR_STORAGE_KEY, next ? "collapsed" : "expanded");
      return next;
    });
  }, [storage]);

  return { layout, setLayout, sidebarCollapsed, toggleSidebar };
}
