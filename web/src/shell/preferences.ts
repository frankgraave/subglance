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

/** Where each preference is persisted. Namespaced like the theme key. */
export const LAYOUT_STORAGE_KEY = "subglance:layout";
export const SIDEBAR_STORAGE_KEY = "subglance:sidebar";

const IDS: readonly string[] = LAYOUTS.map((l) => l.id);

export function isLayoutId(value: unknown): value is LayoutId {
  return typeof value === "string" && IDS.includes(value);
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
