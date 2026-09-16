import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { LayoutSwitcher } from "./LayoutSwitcher";
import { BeakerIcon, PlusIcon, SidebarIcon } from "./icons";
import { setTopbarSlot } from "./topbarSlot";
import type { LayoutId } from "./preferences";

/**
 * The toolbar: collapse the sidebar, choose a layout, switch theme.
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
 * **Two zones, and the split is the point (SUB-131).** The global zone holds
 * what is true on every screen: the sidebar toggle, the theme, the workbench.
 * The page zone holds what belongs to the route you are on, and the layout
 * switcher is a resident of that zone rather than of the bar — it changes how
 * the dashboard's monitors are drawn, and on Incidents it offered a choice
 * between four arrangements of a list that has one. A control that does
 * nothing where it is shown teaches people to stop reading the bar.
 *
 * The page zone is filled two ways, and both are needed. `children` is for a
 * caller that already holds the state, which is how `App` puts the layout
 * switcher here; `TopbarTools` portals into the empty slot from inside the
 * screen, which is how a page contributes controls whose state lives in the
 * page. Same zone, same visual row, no state hoisted into the shell.
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
  /** Opens the add-monitor form. Omitted where there is nothing to add to. */
  onAddMonitor?: () => void;
  addOpen?: boolean;
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
  onAddMonitor,
  addOpen = false,
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
       * The page zone. It carries the slot even when it is empty, because the
       * slot is what `TopbarTools` portals into and a container created only
       * when something wants it would never exist on the render that wants it.
       * An empty flex row occupies no space, so a screen with no tools costs
       * nothing.
       */}
      <div className="shell-topbar-page">
        {children}
        <div ref={setTopbarSlot} className="shell-topbar-slot" />
        {/*
         * Route-owned, so it lives with the route's own controls (SUB-131).
         *
         * It sat in the right-hand group at first, on the argument that a
         * control appearing and disappearing from the middle of the bar
         * slides everything after it sideways. That argument does not apply
         * here: `.shell-topbar-right` carries `margin-left: auto`, so it is
         * anchored to the right edge and does not move when this zone grows
         * or shrinks. What the earlier placement did cost was honesty — the
         * right-hand group is where the controls that are true on every
         * screen live, and a dashboard-only switcher sitting among them says
         * it belongs to the app rather than to one view.
         *
         * The column control stays out of the bar entirely, and that case is
         * genuinely different: it belongs to one *layout* within one screen,
         * and it shared a right-aligned group with the switcher that changes
         * that layout — clicking Rows removed four buttons and slid the
         * button you had just pressed 121px sideways.
         */}
        {showLayouts && onLayoutChange !== undefined && (
          <LayoutSwitcher
            layout={layout}
            effective={effectiveLayout}
            onChange={onLayoutChange}
          />
        )}
      </div>

      <div className="shell-topbar-right">
        {/*
         * First in the group, and an icon button like the rest.
         *
         * Product principle 2 starts with finding this control: a monitoring
         * tool whose primary action is behind a settings page fails the sixty
         * seconds before the form is even reached. It stays an icon rather
         * than a filled button because a permanently green button on every
         * screen becomes wallpaper (DESIGN.md §7.1) — the empty dashboard
         * carries the loud version of the same action instead.
         */}
        {onAddMonitor !== undefined && (
          <button
            type="button"
            className="shell-icon-btn"
            onClick={onAddMonitor}
            aria-pressed={addOpen}
            aria-label="Add a monitor"
            title="Add a monitor"
          >
            <PlusIcon />
          </button>
        )}

        {/*
         * The column control is deliberately NOT here.
         *
         * It lived in this bar for one commit and the cost showed up
         * immediately: it exists only for Cards, so switching to Rows removed
         * four buttons from a right-aligned group and slid everything before
         * them — including the layout switcher the user had just clicked —
         * 121px sideways. Chrome that moves under the cursor is the thing
         * `.panel-row-actions` goes out of its way to avoid.
         *
         * This bar now holds only what is true on every screen. Tools that
         * belong to one view live in the dashboard's own tools row, where
         * appearing and disappearing costs nothing above them.
         */}

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
