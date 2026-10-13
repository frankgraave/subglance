import { PAGE_TITLES } from "./pages";
import type { Route } from "./route";

/**
 * Where a monitor's page was opened from, and so where its back control goes.
 *
 * Two lists open a monitor: the dashboard and the inventory at `/monitors`.
 * The back control used to go to the dashboard whatever the way in, while the
 * breadcrumb on the same page said Monitors and the rail lit Monitors — two
 * "up" controls on one page that went to different places. It now returns to
 * the list the page was opened from, and its label names that list.
 *
 * The origin travels in the history entry's state, not in the URL: a link to
 * a monitor that somebody pastes into a chat has to stay the bare
 * `/monitors/{id}`, and a page reached that way, or from an alert, a new tab
 * or any screen that is not a list, has no list to return to. It goes to
 * `/monitors` then, the breadcrumb's parent, so the two controls agree.
 */

/** The screens that list monitors and open one. */
export type ListName = "dashboard" | "monitors";

export type ListOrigin = {
  list: ListName;
  /**
   * The history index of the list's entry. Returning traverses back to that
   * entry rather than pushing a fresh one, so the list comes back as it was
   * left — its status tab, filters and sort — and the browser's own Back
   * from there goes where it went before the monitor was opened.
   */
  index: number;
};

/**
 * The origin a navigation from `from` to a monitor's page records.
 *
 * A list records itself, however the monitor was opened while it was on
 * screen — its own row, or the command menu over it: the person was on that
 * list, and that is the list they mean by "back". Opening one monitor from
 * another keeps the first page's origin for the same reason. The inventory's
 * add form at `/monitors/new` is not a list to return to (it would reopen
 * the form), and any other screen records none.
 */
export function originFrom(
  from: Route,
  index: number,
  inherited: ListOrigin | null,
): ListOrigin | null {
  if (from.name === "dashboard") return { list: "dashboard", index };
  if (from.name === "monitors" && !from.create) return { list: "monitors", index };
  if (from.name === "monitor") return inherited;
  return null;
}

/**
 * An origin read back out of `history.state`, which outlives a reload and is
 * written by whatever code ran in this tab before: anything that is not
 * exactly the shape above is no origin at all.
 */
export function readOrigin(value: unknown): ListOrigin | null {
  if (typeof value !== "object" || value === null) return null;
  const { list, index } = value as Record<string, unknown>;
  if (list !== "dashboard" && list !== "monitors") return null;
  if (!Number.isInteger(index) || (index as number) < 0) return null;
  return { list, index: index as number };
}

/**
 * The back control's words. They name the destination: the dashboard's own
 * title when that is where it goes, "All monitors" for the inventory, which
 * is also where a page with no origin goes.
 */
export function backLabel(origin: ListOrigin | null): string {
  return origin?.list === "dashboard" ? PAGE_TITLES.dashboard : "All monitors";
}
