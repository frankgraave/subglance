import type { Route } from "./route";
import type { NavRoute } from "./Sidebar";

/**
 * Every screen's name and width, in one table (SUB-182).
 *
 * The UI assessment of 1 October found each screen deciding both for itself:
 * the dashboard titled itself "Monitors" while the tab said "Dashboard", the
 * inventory said "Configured monitors", Settings had no title but its first
 * card, and the content column stopped at three different widths. Each choice
 * was defensible on its own screen, and together they made the product read
 * as five products. A table that the sidebar, the page title and the tab
 * title all read from is what makes “the same name everywhere” structural
 * rather than a convention to remember.
 */

/**
 * The `id` of the masthead's `h1`. It names `<main>`, so a route change that
 * moves focus there announces the page by name, as the heading inside it used
 * to; and it is where a monitor's name lands when its row opens.
 */
export const PAGE_TITLE_ID = "shell-title";

/** A screen's name: the sidebar label, the masthead's title and the tab title. */
export const PAGE_TITLES: Readonly<Record<NavRoute, string>> = {
  dashboard: "Dashboard",
  incidents: "Incidents",
  monitors: "Monitors",
  notifications: "Notifications",
  settings: "Settings",
};

/** The masthead's title and the tab's for an address that names no screen. */
export const NOT_FOUND_TITLE = "Page not found";

/**
 * The page types, by width. One rule underneath all three: **the column of
 * cards is `--size-pane-lg` on every screen but the dashboard.** Adding a
 * type should be a decision recorded here and in AGENTS.md, not a
 * `max-width` in a feature stylesheet.
 *
 * - `full` is the dashboard's: an overview that is watched rather than read,
 *   whose rows, card grid and compact lines use every column they are given,
 *   and whose Cards layout offers to fill the width.
 * - `measure` is a list or a record read top to bottom — monitors,
 *   incidents, notifications, one monitor — held at the reading measure.
 * - `indexed` is `measure` with the page's own index beside it (Settings).
 *   The index is navigation, not content, so it stands to the left of the
 *   column rather than taking width out of it: the cards keep the measure
 *   every other screen's cards have.
 */
export type PageWidth = "full" | "measure" | "indexed";

export type PageFrame = {
  /**
   * The masthead's `h1` (SUB-207). `null` on a monitor's page, whose title is
   * the monitor's name: the table cannot know it, so `App` reads it from the
   * shared monitor list.
   */
  title: string | null;
  width: PageWidth;
};

/** The frame a route is drawn in. */
export function pageFrame(route: Route): PageFrame {
  if (route.name === "monitor") return { title: null, width: "measure" };
  // Not a screen of the product, so not in `PAGE_TITLES` or the rail. Full
  // width because its galleries lay states side by side.
  if (route.name === "workbench") return { title: "Workbench", width: "full" };
  // Not a screen either, and it lights no section of the rail: the address
  // belongs to none of them. Measure, because it is one card read once.
  if (route.name === "notFound") return { title: NOT_FOUND_TITLE, width: "measure" };
  return {
    title: PAGE_TITLES[route.name],
    width:
      route.name === "dashboard"
        ? "full"
        : route.name === "settings"
          ? "indexed"
          : "measure",
  };
}
