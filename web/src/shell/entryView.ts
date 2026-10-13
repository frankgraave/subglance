import { useEffect, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import type { ListName } from "./listOrigin";
import { ROUTE_INDEX } from "./useRoute";

/**
 * A list's view — its status tab, filters, sort — kept on the history entry
 * the list is drawn at.
 *
 * Returning to a list from a monitor's page lands on the list's own entry
 * (`useRoute`'s `backToList`, or the browser's Back), and the list comes back
 * as it was left: narrowing to Down, opening a monitor and coming back to a
 * list that shows everything again would be the kind of reset that makes
 * people stop using the filter.
 *
 * Kept per entry and not per tab, on purpose. A fresh visit — the rail, the
 * command menu — is a new entry with no view, so the list starts unfiltered
 * there: a dashboard that silently still hides 198 of 200 monitors an hour
 * after somebody narrowed it is how an outage gets missed. Returning is
 * remembering; arriving is not.
 *
 * Tagged with the list's name, because `history.state` outlives a reload and
 * is shared by everything that runs in the tab: a view saved by one list is
 * never read by the other, and anything malformed reads as no view at all.
 * Each list validates the fields it reads (`DashboardView`, `InventoryView`).
 */

const VIEW = "subglanceListView";

/** The saved fields of `list`'s view on the current entry, unvalidated. */
export function readEntryView(
  list: ListName,
  history: History = window.history,
): Readonly<Record<string, unknown>> {
  const saved: unknown = history.state?.[VIEW];
  if (typeof saved !== "object" || saved === null) return {};
  const { list: owner, ...fields } = saved as Record<string, unknown>;
  return owner === list ? fields : {};
}

/**
 * Merges `fields` into `list`'s view on the current entry. Replaces the
 * entry's state in place, keeping the router's own keys and the URL, so it
 * adds no history entry and moves nothing the router counts.
 */
export function writeEntryView(
  list: ListName,
  fields: Readonly<Record<string, unknown>>,
  history: History = window.history,
): void {
  const state: Record<string, unknown> =
    typeof history.state === "object" && history.state !== null ? history.state : {};
  try {
    history.replaceState(
      { ...state, [VIEW]: { ...readEntryView(list, history), ...fields, list } },
      "",
      window.location.href,
    );
  } catch {
    // Safari refuses more than a hundred state changes in ten seconds, and
    // a text filter writes one per keystroke. A view that is not kept is a
    // list that comes back unfiltered, which is no reason to crash the list.
  }
}

/**
 * `useState`, kept on the current history entry under `list`'s view.
 *
 * The first render reads the saved value when `valid` accepts it, and every
 * change is written back. With `list` null it is plain `useState`: the
 * workbench draws the same lists from fixtures and has no entry of its own
 * to keep them on.
 *
 * A reload keeps the entry, so it keeps the view too, as a browser keeps a
 * form field's value; the list's tabs and filter note still say what it is
 * narrowed to, so the narrowing is never silent.
 */
export function useEntryState<T>(
  list: ListName | null,
  key: string,
  fallback: T,
  valid: (value: unknown) => value is T,
): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => {
    if (list === null) return fallback;
    const saved = readEntryView(list)[key];
    return valid(saved) ? saved : fallback;
  });
  /*
   * Which entry the list is drawn at, read during render so the write below
   * follows the list onto a new entry. The inventory stays mounted from
   * `/monitors` to `/monitors/new` and back, and closing the add drawer
   * pushes a fresh `/monitors` entry: written only on change, the view would
   * stay behind on the old entry, and the way back from a monitor opened
   * after that would land on a list with its filters gone.
   */
  const entry: unknown = list === null ? null : window.history.state?.[ROUTE_INDEX];
  useEffect(() => {
    if (list !== null) writeEntryView(list, { [key]: value });
  }, [list, key, value, entry]);
  return [value, setValue];
}

/** A string, for a text filter or a key. */
export const isString = (value: unknown): value is string =>
  typeof value === "string";

/** A tag selection: an object of string values, as `TagSelection` is. */
export const isTagSelection = (
  value: unknown,
): value is Readonly<Record<string, string>> =>
  typeof value === "object" &&
  value !== null &&
  !Array.isArray(value) &&
  Object.values(value).every((v) => typeof v === "string");

/** One of `allowed`, for a choice from a fixed list. */
export const oneOf =
  <T,>(allowed: readonly T[]) =>
  (value: unknown): value is T =>
    allowed.includes(value as T);
