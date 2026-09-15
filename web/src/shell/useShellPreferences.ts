import { useCallback, useState } from "react";
import {
  CARD_COLUMNS_STORAGE_KEY,
  LAYOUT_STORAGE_KEY,
  SIDEBAR_STORAGE_KEY,
  readStoredCardColumns,
  readStoredLayout,
  readStoredSidebarCollapsed,
  writePreference,
  type CardColumns,
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
  /**
   * How many cards per row, in the Cards layout only.
   *
   * Stored under its own key rather than folded into the layout id: it has to
   * survive a trip through Rows and back, or switching layouts twice would
   * quietly reset a setting the user chose once.
   */
  cardColumns: CardColumns;
  setCardColumns: (next: CardColumns) => void;
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

  const [cardColumns, setCardColumnsState] = useState<CardColumns>(() =>
    readStoredCardColumns(storage),
  );

  const setLayout = useCallback(
    (next: LayoutId) => {
      setLayoutState(next);
      writePreference(storage, LAYOUT_STORAGE_KEY, next);
    },
    [storage],
  );

  const setCardColumns = useCallback(
    (next: CardColumns) => {
      setCardColumnsState(next);
      writePreference(storage, CARD_COLUMNS_STORAGE_KEY, next);
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

  return {
    layout,
    setLayout,
    cardColumns,
    setCardColumns,
    sidebarCollapsed,
    toggleSidebar,
  };
}
