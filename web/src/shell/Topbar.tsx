import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { LayoutSwitcher } from "./LayoutSwitcher";
import { BeakerIcon, SidebarIcon, SearchIcon } from "./icons";
import { setTopbarSlot } from "./topbarSlot";
import type { LayoutId } from "./preferences";

/**
 * The masthead: the same bar on every screen (SUB-138).
 *
 * Sticky, because at 200 monitors the layout switch is otherwise a scroll away
 * from the rows it changes. It carries no page title — the sidebar already
 * says where you are, and a heading repeated in two pieces of chrome is the
 * kind of decoration rule 1 rejects.
 *
 * The connection banner is rendered *above* this bar by the dashboard itself:
 * a warning that the numbers are frozen has to be read before the numbers, and
 * this is the last chrome before them.
 *
 * **What belongs here is what is true everywhere.** Left: the sidebar toggle,
 * then search. Right: the layout switcher, the workbench, the theme. Nothing
 * in this bar appears on one screen and not another, so moving between screens
 * never moves a control the hand has already learned.
 *
 * That rule is why the add-monitor button left this bar. It sat on the right
 * for a good reason — product principle 2 begins with *finding* the primary
 * action, and a monitoring tool whose add button hides behind a settings page
 * fails the sixty seconds before the form is reached. But a global button
 * cannot be honest about a local action: pressing it on Notifications opened a
 * drawer for adding a *monitor*, on a screen about channels. The principle is
 * unchanged and the answer moved — Monitors carries an "Add monitor" button in
 * its own card header, and the empty dashboard carries the loud version.
 *
 * Tools that belong to one route live in `PageToolbar`, the bar underneath.
 */

export type TopbarProps = {
  sidebarCollapsed: boolean;
  /** True below the breakpoint, where this button opens a drawer. */
  narrow?: boolean;
  onToggleSidebar: () => void;
  /**
   * The layout switcher's state. Omitted where the switcher does not belong —
   * see `showLayouts`.
   */
  layout?: LayoutId;
  effectiveLayout?: LayoutId;
  onLayoutChange?: (next: LayoutId) => void;
  /**
   * Whether this route has layouts to switch between.
   *
   * A prop rather than something inferred here from `onLayoutChange`, because
   * "this screen has no layouts" is a fact about the route and `App` is what
   * knows the route. Inferring it from a missing callback would make the bar's
   * contents depend on how carefully a caller spelled its props.
   */
  showLayouts?: boolean;
  themePreference: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  workbenchOpen: boolean;
  onToggleWorkbench: () => void;
  onOpenCommands?: () => void;
  /** Optional extra controls, e.g. a search field owned by the page. */
  children?: ReactNode;
};

export function Topbar({
  sidebarCollapsed,
  narrow = false,
  onToggleSidebar,
  layout = "rows",
  effectiveLayout,
  onLayoutChange,
  showLayouts = false,
  themePreference,
  onThemeChange,
  workbenchOpen,
  onToggleWorkbench,
  onOpenCommands,
  children,
}: TopbarProps) {
  return (
    <header className="shell-topbar">
      <button
        type="button"
        className="shell-icon-btn"
        onClick={onToggleSidebar}
        aria-expanded={!sidebarCollapsed}
        // The name says what pressing it will do, and on a phone that is not
        // the same action: there is no rail to collapse, there is a drawer to
        // open. A label that says "Collapse sidebar" beside a screen with no
        // sidebar is a small lie told by the one control that has to be
        // trusted to lead somewhere.
        //
        // The shortcut stays in the accessible name on the desktop, where a
        // keyboard exists: shortcuts that exist but are undiscoverable are a
        // known gap in DESIGN.md §12, and this is the cheapest half of it.
        aria-label={
          narrow
            ? sidebarCollapsed
              ? "Open navigation"
              : "Close navigation"
            : `${sidebarCollapsed ? "Expand" : "Collapse"} sidebar (Ctrl+B)`
        }
        title={
          narrow
            ? sidebarCollapsed
              ? "Open navigation"
              : "Close navigation"
            : `${sidebarCollapsed ? "Expand" : "Collapse"} sidebar — Ctrl/Cmd + B`
        }
      >
        <SidebarIcon />
      </button>

      {/*
       * Search sits beside the sidebar toggle on every screen, because what it
       * searches is always "the things this screen lists". The field is
       * rendered by the screen — the query belongs to the list it filters —
       * and portalled in through `TopbarTools`, so the position is the
       * shell's and the state stays in the page.
       *
       * The slot exists even when empty: it is what the portal attaches to,
       * and a container created only when something wants it would never
       * exist on the render that wants it. An empty flex child occupies no
       * space, so a screen without search costs nothing.
       */}
      <div ref={setTopbarSlot} className="shell-topbar-search" />
      {onOpenCommands && <button type="button" className="shell-icon-btn shell-command-launcher" aria-label="Open command menu" aria-keyshortcuts="Control+K Meta+K" title="Command menu — Ctrl/Cmd + K" onClick={onOpenCommands}><span className="shell-search-kbd" aria-hidden="true">⌘K</span><SearchIcon /></button>}

      {/* Anything else a caller wants in the masthead, which is currently
          nothing: kept because `children` is the escape hatch for a control
          that is genuinely global and does not fit the three groups above. */}
      {children}

      <div className="shell-topbar-right">
        {/*
         * The layout switcher is global chrome, not a page tool (SUB-138).
         *
         * It was moved into the page zone earlier on the argument that it
         * changes the dashboard and therefore belongs to it. Frank's rule is
         * simpler and holds better: the masthead is identical everywhere, so
         * a control that is in it must never move. Rows/Cards/Compact/Status
         * wall is how *this product* draws a list of monitors, and it reads
         * as an app-level preference rather than as a dashboard filter.
         *
         * On a route with one arrangement it renders nothing at all rather
         * than offering a choice that changes nothing — `showLayouts` is the
         * route's answer, not this component's guess.
         */}
        {showLayouts && onLayoutChange !== undefined && (
          <LayoutSwitcher
            layout={layout}
            effective={effectiveLayout}
            onChange={onLayoutChange}
          />
        )}

        {/*
         * The workbench survives, as a side track rather than a tab beside the
         * product. Judging a component in isolation and in both themes is
         * something the live screen cannot do — it only ever shows the states
         * the server happens to be in — but it is a developer tool, and the
         * first thing you land on must be the real dashboard.
         */}
        <button
          type="button"
          className="shell-icon-btn"
          onClick={onToggleWorkbench}
          aria-pressed={workbenchOpen}
          aria-label="Component workbench"
          title="Component workbench — every state, both themes"
        >
          <BeakerIcon />
        </button>

        <ThemeToggle preference={themePreference} onChange={onThemeChange} />
      </div>
    </header>
  );
}
