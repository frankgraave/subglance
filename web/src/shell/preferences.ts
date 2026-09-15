/**
 * Shell preferences: which layout the dashboard is in, and whether the
 * sidebar is collapsed.
 *
 * Pure and storage-agnostic, for the same reason `theme.ts` is: the part worth
 * testing is the narrowing of untrusted input (a `localStorage` value written
 * by an older build, or by hand), not the act of calling `getItem`.
 *
 * Layout is a user setting rather than a route or a breakpoint (DESIGN.md §7).
 * A route would make "how I like to look at my monitors" a place you can be
 * linked to and land in someone else's preference; a breakpoint would take the
 * choice away entirely.
 */

export type LayoutId = "rows" | "cards" | "compact" | "wall";

/** The layouts, in the order the toolbar offers them (DESIGN.md §7). */
export const LAYOUTS: readonly { id: LayoutId; label: string; hint: string }[] = [
  { id: "rows", label: "Rows", hint: "One row per monitor. For scanning on a laptop." },
  { id: "cards", label: "Cards", hint: "Tiles with bigger lamps. For a second screen." },
  { id: "compact", label: "Compact", hint: "One dense line per monitor. For 100+." },
  { id: "wall", label: "Status wall", hint: "Lamp and name only. For a screen on the wall." },
];

export const DEFAULT_LAYOUT: LayoutId = "rows";

/**
 * How many cards the Cards layout puts on a row.
 *
 * `"auto"` is not a fifth number: it fills the width with a floor, so a wide
 * screen decides for itself and a narrow one still gets a single column. The
 * numbers exist beside it because "decide for me" and "give me exactly two"
 * are different requests, and a monitor wall is usually the second.
 */
export type CardColumns = "1" | "2" | "3" | "auto";

/**
 * The options, in the order the toolbar offers them.
 *
 * Every entry carries a `hint`: the control is drawn as bars rather than
 * words (§7.8), so the `title` is the only thing that names what a button
 * does for anyone who does not read the icon the way we drew it.
 */
export const CARD_COLUMNS: readonly { id: CardColumns; hint: string }[] = [
  { id: "1", hint: "One card per row." },
  { id: "2", hint: "Two cards per row." },
  { id: "3", hint: "Three cards per row." },
  { id: "auto", hint: "As many as fit the window." },
];

export const DEFAULT_CARD_COLUMNS: CardColumns = "2";

/** Where each preference is persisted. Namespaced like the theme key. */
export const LAYOUT_STORAGE_KEY = "subglance:layout";
export const SIDEBAR_STORAGE_KEY = "subglance:sidebar";
export const CARD_COLUMNS_STORAGE_KEY = "subglance:card-columns";

const IDS: readonly string[] = LAYOUTS.map((l) => l.id);
const COLUMN_IDS: readonly string[] = CARD_COLUMNS.map((c) => c.id);

export function isLayoutId(value: unknown): value is LayoutId {
  return typeof value === "string" && IDS.includes(value);
}

export function isCardColumns(value: unknown): value is CardColumns {
  return typeof value === "string" && COLUMN_IDS.includes(value);
}

/**
 * Reads a stored value, treating any storage failure as "no preference".
 *
 * Safari in private mode throws on access. A layout is not worth taking the
 * dashboard down for — the same call the theme makes.
 */
function read(storage: Pick<Storage, "getItem">, key: string): string | null {
  try {
    return storage.getItem(key);
  } catch {
    return null;
  }
}

/** Best-effort write; a preference that cannot be saved is not an error. */
export function writePreference(
  storage: Pick<Storage, "setItem">,
  key: string,
  value: string,
): void {
  try {
    storage.setItem(key, value);
  } catch {
    // Non-fatal: the choice still applies for this session.
  }
}

/** The stored layout, falling back to rows for absent or corrupt values. */
export function readStoredLayout(storage: Pick<Storage, "getItem">): LayoutId {
  const raw = read(storage, LAYOUT_STORAGE_KEY);
  return isLayoutId(raw) ? raw : DEFAULT_LAYOUT;
}

/**
 * The stored sidebar state.
 *
 * Expanded is the default: someone who has never expressed a preference should
 * see the navigation, not guess at five icons. Only the literal string
 * "collapsed" collapses it, so an unrecognised value fails open.
 */
export function readStoredSidebarCollapsed(storage: Pick<Storage, "getItem">): boolean {
  return read(storage, SIDEBAR_STORAGE_KEY) === "collapsed";
}

/** The stored column count, falling back to two for absent or corrupt values. */
export function readStoredCardColumns(
  storage: Pick<Storage, "getItem">,
): CardColumns {
  const raw = read(storage, CARD_COLUMNS_STORAGE_KEY);
  return isCardColumns(raw) ? raw : DEFAULT_CARD_COLUMNS;
}

/**
 * The layout actually rendered, once the viewport has had its say.
 *
 * The preference is honoured wherever it can be: `wall` and `cards` work at
 * every width. `rows` and `compact` cannot — both put five columns of facts on
 * one line, which is the exact thing that does not fit below 640px
 * (DESIGN.md §13). Rather than hand a phone a horizontally scrolling table we
 * fall back to cards, which keeps every fact the row shows.
 *
 * The stored preference is deliberately *not* rewritten when this happens:
 * opening the dashboard on a phone must not silently change what the desktop
 * shows tomorrow.
 */
export function effectiveLayout(preference: LayoutId, narrow: boolean): LayoutId {
  if (!narrow) return preference;
  return preference === "rows" || preference === "compact" ? "cards" : preference;
}
